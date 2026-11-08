// Minimal HTTP wrapper around the Dolos library (https://dolos.ugent.be) for the autograder.
// POST /analyze {language, files: [{name, content}], maxFingerprintPercentage?}
//   -> {pairs: [{left, right, similarity, dolosSimilarity, leftTotal, rightTotal, fragments: [{left: [startRow, endRow], right: [...], kgrams}]}]}
// minKgrams (optional) overrides the shortest kept match, for plain text ("char").
// Rows are 0-based. Runs without network access; requests are handled one at a time to bound memory.
//
// Dolos pairs every occurrence of a shared k-gram, so one block of code that follows a common pattern (a method with a
// logging call) "matches" every similar method of the other work. Like Greedy String Tiling, fragments are kept one to
// one, longest first, without overlaps on either side, and fragments shorter than minKgrams are dropped; similarity is
// recomputed from what is kept, with the same denominator Dolos uses.
import http from "node:http";
import { Dolos, File } from "/repo/lib/dist/index.js";

const maxBody = 200 << 20;
const minKgrams = 8; // ~30 tokens, 3-5 lines: shorter overlaps are common idioms, not copying

function tile(p, min = minKgrams) {
  const kept = [];
  for (const f of p.buildFragments().sort((a, b) => b.leftkgrams.length - a.leftkgrams.length)) {
    if (f.leftkgrams.length < min || f.rightkgrams.length < min) continue;
    if (kept.some(k => k.leftkgrams.overlapsWith(f.leftkgrams) || k.rightkgrams.overlapsWith(f.rightkgrams))) continue;
    kept.push(f);
  }
  const covered = kept.reduce((n, f) => n + f.leftkgrams.length + f.rightkgrams.length, 0);
  const denominator = p.leftTotal + p.rightTotal - (p.leftIgnored ?? 0) - (p.rightIgnored ?? 0);
  return { kept, similarity: denominator > 0 ? Math.min(1, covered / denominator) : 0 };
}
let queue = Promise.resolve();

async function analyze(req) {
  const files = req.files.map((f, i) => new File(f.name, f.content, undefined, i));
  const dolos = new Dolos({ language: req.language, maxFingerprintPercentage: req.maxFingerprintPercentage ?? null });
  const report = await dolos.analyze(files);
  return {
    warnings: report.warnings ?? [],
    pairs: report.allPairs().map(p => {
      const { kept, similarity } = tile(p, Number.isInteger(req.minKgrams) && req.minKgrams > 0 ? req.minKgrams : minKgrams);
      return {
        left: p.leftFile.path, right: p.rightFile.path, similarity, dolosSimilarity: p.similarity, leftTotal: p.leftTotal, rightTotal: p.rightTotal,
        fragments: kept.map(f => ({ left: [f.leftSelection.startRow, f.leftSelection.endRow], right: [f.rightSelection.startRow, f.rightSelection.endRow], kgrams: f.leftkgrams.length })),
      };
    }),
  };
}

http.createServer((req, res) => {
  const reply = (code, body) => { res.writeHead(code, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
  if (req.method === "GET" && req.url === "/health") return reply(200, { ok: true });
  if (req.method !== "POST" || req.url !== "/analyze") return reply(404, { error: "not found" });
  const chunks = []; let size = 0;
  req.on("data", c => { size += c.length; if (size > maxBody) { reply(413, { error: "request too large" }); req.destroy(); } else chunks.push(c); });
  req.on("end", () => {
    if (size > maxBody) return;
    queue = queue.then(async () => {
      try {
        const body = JSON.parse(Buffer.concat(chunks).toString("utf8"));
        if (!Array.isArray(body.files) || body.files.length < 2) return reply(200, { pairs: [], warnings: [] });
        reply(200, await analyze(body));
      } catch (e) {
        reply(422, { error: String(e?.message ?? e) });
      }
    });
  });
}).listen(8080, "0.0.0.0", () => console.log("dolos service listening on :8080"));
