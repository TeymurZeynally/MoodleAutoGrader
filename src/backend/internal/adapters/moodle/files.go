package moodle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ErrForeignHost means a file URL points outside the Moodle site; the token is never sent there.
var ErrForeignHost = errors.New("file url is not on the moodle site")

type EnrolledUser struct {
	ID        int64  `json:"id"`
	FullName  string `json:"fullname"`
	FirstName string `json:"firstname"` // often "Имя Отчество" on Russian sites
	LastName  string `json:"lastname"`
	Groups    []struct {
		Name string `json:"name"`
	} `json:"groups"` // course groups, usually synced from cohorts ("global groups")
	Roles []struct {
		Shortname string `json:"shortname"`
	} `json:"roles"`
}

func (u EnrolledUser) IsStudent() bool {
	for _, r := range u.Roles {
		if r.Shortname == "student" {
			return true
		}
	}

	return false
}

func (u EnrolledUser) GroupNames() []string {
	names := make([]string, len(u.Groups))
	for i, g := range u.Groups {
		names[i] = g.Name
	}

	return names
}

func (r *Reader) EnrolledUsers(ctx context.Context, courseID int64) ([]EnrolledUser, error) {
	var users []EnrolledUser
	err := r.c.Call(ctx, "core_enrol_get_enrolled_users", url.Values{"courseid": {strconv.FormatInt(courseID, 10)}}, &users)
	return users, err
}

// Download fetches a submission file. The token goes in the POST body (webservice/pluginfile.php accepts it there).
func (r *Reader) Download(ctx context.Context, fileURL string, maxBytes int64) ([]byte, error) {
	u, err := url.Parse(fileURL)
	if err != nil {
		return nil, fmt.Errorf("parse file url: %w", err)
	}

	base, _ := url.Parse(r.c.baseURL)
	if u.Host != base.Host || u.Scheme != base.Scheme || !strings.Contains(u.Path, "/pluginfile.php/") {
		return nil, fmt.Errorf("%s: %w", u.Host, ErrForeignHost)
	}

	q := u.Query()
	q.Del("token")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(url.Values{"token": {r.c.token}}.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := r.c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: http status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}

	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("download: file exceeds %d bytes", maxBytes)
	}

	// Moodle reports errors as JSON even here (e.g. invalid token).
	var mErr struct{ ErrorCode, Error string }
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") && json.Unmarshal(data, &mErr) == nil && mErr.ErrorCode != "" {
		return nil, &Error{Function: "pluginfile", Code: mErr.ErrorCode, Message: mErr.Error}
	}

	return data, nil
}

// PublishFunctions is the moodle-publisher role allowlist.
var PublishFunctions = []string{"mod_assign_save_grade"}

// Publisher can only save grades; it has no read or download access.
type Publisher struct{ c *Client }

func NewPublisher(baseURL, token string, opts ...Option) *Publisher {
	return &Publisher{c: NewClient(baseURL, token, PublishFunctions, opts...)}
}

// SaveGrade sets the grade and feedback comment for the latest attempt of a student.
func (p *Publisher) SaveGrade(ctx context.Context, assignmentID, userID int64, grade float64, comment string) error {
	params := url.Values{
		"assignmentid": {strconv.FormatInt(assignmentID, 10)}, "userid": {strconv.FormatInt(userID, 10)}, "grade": {fmt.Sprint(grade)},
		"attemptnumber": {"-1"}, "addattempt": {"0"}, "workflowstate": {""}, "applytoall": {"0"},
		"plugindata[assignfeedbackcomments_editor][text]": {comment}, "plugindata[assignfeedbackcomments_editor][format]": {"1"},
	}
	var out any
	return p.c.Call(ctx, "mod_assign_save_grade", params, &out)
}

// ExchangePassword trades a username and password for a moodle_mobile_app token. The password is not kept.
func ExchangePassword(ctx context.Context, baseURL, username, password string, opts ...Option) (string, error) {
	c := NewClient(baseURL, "", nil, opts...)
	form := url.Values{"username": {username}, "password": {password}, "service": {"moodle_mobile_app"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/login/token.php", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	var body struct{ Token, Error, ErrorCode string }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	if body.Token == "" {
		return "", &Error{Function: "login/token.php", Code: body.ErrorCode, Message: body.Error}
	}

	return body.Token, nil
}
