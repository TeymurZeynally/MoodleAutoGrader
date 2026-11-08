// autograder is the main binary. Each role is a process of its own, with only the secrets and the network it needs:
//
//	autograder migrate        apply database migrations and prepare the role the others connect with; only this one has
//	                          the password of the database owner, and it exits when done
//	autograder web            the HTTP API with the web UI: has the session key, never reads the files of students
//	autograder worker-moodle  reads from and publishes to Moodle: has the session key, stores files as opaque bytes
//	autograder worker-core    reads the files of students, compares them, runs the AI check: no session key, no internet
//	autograder seed-export <course id> write the prepared data of a course, by its id in Moodle, to stdout as a tar archive
//
// A course's data for colleagues, from deploy/ of a running stack:
//
//	mkdir -p seed && docker compose exec -T app /autograder seed-export 12345 | tar -xC seed
//
// Configuration comes from the environment; *_FILE variables point to files with secrets.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata" // the distroless image has no zoneinfo; TZ sets local time for student comments.

	"moodle-autograder/internal/adapters/dolos"
	"moodle-autograder/internal/adapters/filestore"
	"moodle-autograder/internal/adapters/llm"
	"moodle-autograder/internal/adapters/moodle"
	"moodle-autograder/internal/adapters/ollama"
	"moodle-autograder/internal/adapters/postgres"
	"moodle-autograder/internal/adapters/runner"
	"moodle-autograder/internal/adapters/seedfiles"
	"moodle-autograder/internal/adapters/tools"
	"moodle-autograder/internal/adapters/uploads"
	"moodle-autograder/internal/app/access"
	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/app/comparing"
	"moodle-autograder/internal/app/courses"
	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/app/seeding"
	"moodle-autograder/internal/app/syncing"
	"moodle-autograder/internal/app/viewing"
	"moodle-autograder/internal/delivery/httpapi"
	"moodle-autograder/internal/delivery/session"
	"moodle-autograder/internal/delivery/workflows"

	"go.temporal.io/sdk/client"
)

// localOptions is what one installation needs from its own file, kept out of the repository.
var localOptions []moodle.Option

var errUsage = errors.New("usage: autograder migrate | web | worker-moodle | worker-core | seed-export <moodle course id>")

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("autograder failed", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(args) == 0 {
		return errUsage
	}

	dsn, err := databaseURL()
	if err != nil {
		return err
	}

	switch args[0] {
	case "migrate":
		return migrate(ctx, dsn)
	case "web":
		return runWeb(ctx, dsn)
	case "worker-moodle":
		return runMoodleWorker(ctx, dsn)
	case "worker-core":
		return runCoreWorker(ctx, dsn)
	case "seed-export":
		if len(args) != 2 {
			return errUsage
		}
		return seedExport(ctx, dsn, args[1])
	}

	return fmt.Errorf("unknown command %q: %w", args[0], errUsage)
}

// migrate runs as the database owner. Without the password of the running processes it only applies the migrations:
// that is how the schema snapshot is generated.
func migrate(ctx context.Context, dsn string) error {
	n, err := postgres.Migrate(ctx, dsn)
	if err != nil {
		return err
	}

	slog.Info("migrations applied", "count", n)
	if pw := secret("APP_DB_PASSWORD"); pw != "" {
		return postgres.PrepareAppRole(ctx, dsn, pw)
	}

	return nil
}

func seedExport(ctx context.Context, dsn, moodleCourse string) error {
	id, err := strconv.ParseInt(moodleCourse, 10, 64)
	if err != nil {
		return fmt.Errorf("the course's id in Moodle: %w", err)
	}

	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer store.Close()
	site, err := moodleSite(ctx, store)
	if err != nil {
		return err
	}

	s := &seeding.Service{Store: store, Site: site}
	c, err := s.Export(ctx, id)
	if err != nil {
		return err
	}

	return seedfiles.WriteTar(os.Stdout, id, c)
}

// moodleSite is the Moodle site of this installation: MOODLE_URL, or the one the first sign-in set; empty before it.
func moodleSite(ctx context.Context, store *postgres.Store) (string, error) {
	if site := strings.TrimRight(os.Getenv("MOODLE_URL"), "/"); site != "" {
		return site, nil
	}

	return store.MoodleSite(ctx)
}

// waitForSite returns the Moodle site once the first sign-in has set it.
func waitForSite(ctx context.Context, store *postgres.Store) (string, error) {
	for waited := false; ; waited = true {
		site, err := moodleSite(ctx, store)
		if err != nil || site != "" {
			return site, err
		}

		if !waited {
			slog.Info("no Moodle site yet: waiting for the first sign-in to set it")
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// firstSite keeps the site of the first sign-in and switches the web server over to it.
type firstSite struct {
	store *postgres.Store
	then  func(site string)
}

func (f firstSite) SetMoodleSite(ctx context.Context, url string) (string, error) {
	kept, err := f.store.SetMoodleSite(ctx, url)
	if err == nil {
		f.then(kept)
	}

	return kept, err
}

// services is what every role connects to. The composition root: every use case gets its adapters in this file, by hand.
// Nothing else creates adapters.
type services struct {
	store      *postgres.Store
	uploads    *uploads.Store
	tools      *tools.Client
	temporal   client.Client
	background runner.Temporal
}

func connect(ctx context.Context, dsn string) (*services, func(), error) {
	store, err := postgres.Open(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}

	files, err := filestore.New(ctx, env("S3_ENDPOINT", "seaweedfs:8333"), secret("S3_ACCESS_KEY"), secret("S3_SECRET_KEY"), env("S3_SECURE", "") == "1")
	if err != nil {
		store.Close()
		return nil, nil, err
	}

	tc, err := client.DialContext(ctx, client.Options{HostPort: env("TEMPORAL_ADDRESS", "temporal:7233"), Logger: nil})
	if err != nil {
		store.Close()
		return nil, nil, fmt.Errorf("connect temporal: %w", err)
	}

	docs := tools.New(env("TOOLS_URL", "http://tools:8080"))
	s := &services{store: store, uploads: &uploads.Store{Files: files, Tools: docs}, tools: docs, temporal: tc, background: runner.Temporal{Client: tc}}
	return s, func() { tc.Close(); store.Close() }, nil
}

func newSealer() (*session.Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(secret("SESSION_KEY")))
	if err != nil {
		return nil, fmt.Errorf("decode SESSION_KEY: %w", err)
	}

	return session.NewSealer(key)
}

func newAssessing(s *services) (*assessing.Service, error) {
	assess := &assessing.Service{Store: s.store, Uploads: s.uploads, Prompts: llm.Catalog{}, Runner: s.background}
	switch p := env("LLM_PROVIDER", "none"); p {
	case "none":
		slog.Info("AI check disabled; drafts come from integrity checks only")
	case "ollama":
		options, err := seedfiles.Dir(env("SEED_DIR", "")).ModelOptions()
		if err != nil {
			return nil, err
		}
		models := ollama.New(env("OLLAMA_URL", "http://ollama:11434"), options)
		assess.Assessors, assess.Models = llm.New(models).Session, models
		slog.Info("AI check with local models; models per step are set on the Prompts page")
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER %q (none, ollama)", p)
	}

	return assess, nil
}

// runWeb serves the API and the UI. The files of students reach it only as bytes to show: toolsd reads the archives. A new
// installation serves the sign-in only, until the first sign-in sets the Moodle site.
func runWeb(ctx context.Context, dsn string) error {
	s, done, err := connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer done()
	sealer, err := newSealer()
	if err != nil {
		return err
	}

	assess, err := newAssessing(s)
	if err != nil {
		return err
	}

	site, err := moodleSite(ctx, s.store)
	if err != nil {
		return err
	}

	cookieSecure := env("COOKIE_SECURE", "1") == "1"
	forSite := func(site string) http.Handler {
		mdl := &moodle.Gateway{URL: site, Opts: localOptions}
		api := &httpapi.Server{
			Access: &access.Service{Store: s.store, Moodle: mdl, Site: site}, Courses: &courses.Service{Store: s.store, Moodle: mdl, Runs: s.background, Site: site},
			Syncing: &syncing.Service{Store: s.store, Runner: s.background}, Grading: &grading.Service{Store: s.store, Moodle: mdl, Runner: s.background, MoodleURL: site},
			Assessing: assess, Comparing: &comparing.Service{Store: s.store, Snapshots: s.uploads},
			Viewing: &viewing.Service{Store: s.store, Uploads: s.uploads, Archives: tools.Archives{Client: s.tools}},
			Sealer:  sealer, CookieSecure: cookieSecure,
		}
		return api.Handler()
	}
	var web atomic.Pointer[http.Handler] // the sign-in of a new installation, then the whole API of its site
	use := func(h http.Handler) { web.Store(&h) }
	if site != "" {
		use(forSite(site))
	} else {
		first := &httpapi.Server{Access: &access.Service{Store: s.store, Moodle: &moodle.Gateway{Opts: localOptions}, Sites: firstSite{s.store, func(site string) { use(forSite(site)) }}}, Sealer: sealer, CookieSecure: cookieSecure}
		use(first.SignInHandler())
		slog.Info("no Moodle site yet: the first sign-in sets it")
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { (*web.Load()).ServeHTTP(w, r) })
	srv := &http.Server{Addr: env("LISTEN", ":8080"), Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: time.Minute, IdleTimeout: 2 * time.Minute}
	go func() { //nolint:gosec // G118: ctx is already cancelled here, the shutdown needs its own time
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("serving", "addr", srv.Addr, "moodle", site)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}

	return nil
}

// runMoodleWorker talks to Moodle with the teacher's token; what it downloads it only stores, without reading it.
func runMoodleWorker(ctx context.Context, dsn string) error {
	s, done, err := connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer done()
	sealer, err := newSealer()
	if err != nil {
		return err
	}

	site, err := waitForSite(ctx, s.store)
	if err != nil {
		return err
	}

	mdl := &moodle.Gateway{URL: site, Opts: localOptions}
	seeds := &seeding.Service{Store: s.store, Source: seedfiles.Dir(env("SEED_DIR", "")), Prompts: llm.Catalog{}, Site: site}
	acts := &workflows.Activities{Syncing: &syncing.Service{Store: s.store, Moodle: mdl, Uploads: s.uploads, Seeds: seeds}, Grading: &grading.Service{Store: s.store, Moodle: mdl, MoodleURL: site}, Sealer: sealer}
	return workflows.RunWorkers(ctx, s.temporal, acts, workflows.MoodleQueues)
}

// runCoreWorker reads what students uploaded. It holds no key to the sessions and has no way to the internet, so a file
// that breaks a parser finds neither a token to steal nor a place to send it.
func runCoreWorker(ctx context.Context, dsn string) error {
	s, done, err := connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer done()
	assess, err := newAssessing(s)
	if err != nil {
		return err
	}

	compare := &comparing.Service{Store: s.store, Snapshots: s.uploads}
	if u := env("DOLOS_URL", ""); u != "" {
		compare.Code = dolos.New(u)
		slog.Info("code similarity by Dolos", "url", u)
	}

	acts := &workflows.Activities{Syncing: &syncing.Service{Store: s.store, Uploads: s.uploads}, Comparing: compare, Assess: assess}
	return workflows.RunWorkers(ctx, s.temporal, acts, workflows.CoreQueues)
}

func databaseURL() (string, error) {
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		return dsn, nil
	}

	pw := secret("POSTGRES_PASSWORD")
	if pw == "" {
		return "", errors.New("DATABASE_URL or POSTGRES_PASSWORD(_FILE) is required")
	}

	u := url.URL{Scheme: "postgres", User: url.UserPassword(env("POSTGRES_USER", postgres.AppRole), pw), Host: env("POSTGRES_HOST", "postgres:5432"), Path: env("POSTGRES_DB", "autograder"), RawQuery: "sslmode=disable"}
	return u.String(), nil
}

// secret reads NAME, or the file at NAME_FILE.
func secret(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}

	if path := os.Getenv(name + "_FILE"); path != "" {
		if b, err := os.ReadFile(path); err == nil { //nolint:gosec // G304: the path comes from the deployment's environment, not from a user
			return strings.TrimSpace(string(b))
		}
	}

	return ""
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}

	return def
}
