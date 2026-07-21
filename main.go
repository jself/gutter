package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

//go:embed index.html
var assets embed.FS

type Config struct {
	Rev      string `json:"rev,omitempty"`
	Output   string `json:"output,omitempty"`
	Dir      string `json:"dir,omitempty"`
	Port     int    `json:"port,omitempty"`
	Open     bool   `json:"open,omitempty"`
	Editor   string `json:"editor,omitempty"`
	Collapse int    `json:"collapse,omitempty"`
	PR       string `json:"pr,omitempty"`
	Sync     bool   `json:"sync,omitempty"`
	MD       string `json:"md,omitempty"`
}

func defaultConfig() Config {
	return Config{Output: "review.md", Open: true, Collapse: 80}
}

func loadConfig() Config {
	c := defaultConfig()
	// User config: $XDG_CONFIG_HOME/gutter/config.json or ~/.config/gutter/config.json
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		if h, err := os.UserHomeDir(); err == nil {
			xdg = filepath.Join(h, ".config")
		}
	}
	if xdg != "" {
		mergeConfigFile(&c, filepath.Join(xdg, "gutter", "config.json"))
	}
	// Project config: ./.gutter.json
	mergeConfigFile(&c, ".gutter.json")
	// Env overrides
	if v := os.Getenv("GUTTER_REV"); v != "" {
		c.Rev = v
	}
	if v := os.Getenv("GUTTER_OUTPUT"); v != "" {
		c.Output = v
	}
	if v := os.Getenv("GUTTER_DIR"); v != "" {
		c.Dir = v
	}
	if v := os.Getenv("GUTTER_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Port = n
		}
	}
	if v := os.Getenv("GUTTER_OPEN"); v != "" {
		c.Open = v != "0" && v != "false" && v != "no"
	}
	if v := os.Getenv("GUTTER_EDITOR"); v != "" {
		c.Editor = v
	}
	if v := os.Getenv("GUTTER_COLLAPSE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Collapse = n
		}
	}
	if v := os.Getenv("GUTTER_PR"); v != "" {
		c.PR = v
	}
	if v := os.Getenv("GUTTER_SYNC"); v != "" {
		c.Sync = v != "0" && v != "false" && v != "no"
	}
	if v := os.Getenv("GUTTER_MD"); v != "" {
		c.MD = v
	}
	return c
}

func mergeConfigFile(c *Config, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var f Config
	// Default Open to current value so a missing key doesn't flip it to false.
	f.Open = c.Open
	if err := json.Unmarshal(b, &f); err != nil {
		fmt.Fprintf(os.Stderr, "gutter: ignoring invalid config %s: %v\n", path, err)
		return
	}
	if f.Rev != "" {
		c.Rev = f.Rev
	}
	if f.Output != "" {
		c.Output = f.Output
	}
	if f.Dir != "" {
		c.Dir = f.Dir
	}
	if f.Port != 0 {
		c.Port = f.Port
	}
	c.Open = f.Open
	if f.Editor != "" {
		c.Editor = f.Editor
	}
	if f.Collapse != 0 {
		c.Collapse = f.Collapse
	}
	if f.PR != "" {
		c.PR = f.PR
	}
	if f.Sync {
		c.Sync = true
	}
	if f.MD != "" {
		c.MD = f.MD
	}
}

type File struct {
	Path      string `json:"path"`
	Lang      string `json:"lang"`
	Hunks     []Hunk `json:"hunks"`
	AddCount  int    `json:"add_count"`
	DelCount  int    `json:"del_count"`
	Untracked bool   `json:"untracked,omitempty"`
}

var extLang = map[string]string{
	".go": "go", ".py": "python", ".js": "javascript", ".jsx": "javascript",
	".ts": "typescript", ".tsx": "typescript", ".rs": "rust", ".rb": "ruby",
	".java": "java", ".kt": "kotlin", ".swift": "swift", ".c": "c", ".h": "c",
	".cc": "cpp", ".cpp": "cpp", ".hpp": "cpp", ".cs": "csharp", ".php": "php",
	".sh": "bash", ".zsh": "bash", ".bash": "bash", ".fish": "bash",
	".html": "xml", ".xml": "xml", ".css": "css", ".scss": "scss",
	".md": "markdown", ".yml": "yaml", ".yaml": "yaml", ".toml": "ini",
	".ini": "ini", ".json": "json", ".sql": "sql", ".lua": "lua",
	".ex": "elixir", ".exs": "elixir", ".erl": "erlang", ".clj": "clojure",
	".scala": "scala", ".dart": "dart", ".hs": "haskell", ".ml": "ocaml",
	".vim": "vim", ".dockerfile": "dockerfile",
}

func langFor(path string) string {
	base := strings.ToLower(filepath.Base(path))
	if base == "dockerfile" {
		return "dockerfile"
	}
	if base == "makefile" {
		return "makefile"
	}
	ext := strings.ToLower(filepath.Ext(path))
	return extLang[ext]
}

type Hunk struct {
	Header   string `json:"header"`
	OldStart int    `json:"old_start"`
	NewStart int    `json:"new_start"`
	Lines    []Line `json:"lines"`
}

type Line struct {
	Kind     string    `json:"kind"` // "ctx", "add", "del"
	OldLine  int       `json:"old_line,omitempty"`
	NewLine  int       `json:"new_line,omitempty"`
	Text     string    `json:"text"`
	Segments []Segment `json:"segments,omitempty"`
}

type Segment struct {
	Kind string `json:"kind"` // "same", "ins", "del"
	Text string `json:"text"`
}

type DiffData struct {
	Rev      string    `json:"rev"`
	VCS      string    `json:"vcs"`
	Files    []File    `json:"files"`
	Prior    []Comment `json:"prior"`
	PriorGen string    `json:"prior_general,omitempty"`
	PR       *PRInfo   `json:"pr,omitempty"`
}

type DocBlock struct {
	HTML      string `json:"html"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Source    string `json:"source"`
}

type Doc struct {
	Path   string     `json:"path"`
	Blocks []DocBlock `json:"blocks"`
}

type Comment struct {
	Path     string `json:"path"`
	Side     string `json:"side"` // "old" or "new"
	Line     int    `json:"line"`
	EndLine  int    `json:"end_line,omitempty"`
	Snippet  string `json:"snippet"`
	Body     string `json:"body"`
}

type SaveRequest struct {
	Comments []Comment `json:"comments"`
	General  string    `json:"general"`
}

type PRInfo struct {
	Repo   string // "owner/name"
	Number int
	Head   string // head commit SHA
	Base   string // base commit SHA
}

// parsePRView turns `gh pr view <pr> --json number,headRefOid,baseRefOid,url`
// output into a PRInfo. Repo ("owner/name") is derived from the canonical url.
func parsePRView(b []byte) (PRInfo, error) {
	var v struct {
		Number     int    `json:"number"`
		HeadRefOid string `json:"headRefOid"`
		BaseRefOid string `json:"baseRefOid"`
		URL        string `json:"url"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return PRInfo{}, fmt.Errorf("parsing gh pr view output: %w", err)
	}
	repo, err := repoFromPRURL(v.URL)
	if err != nil {
		return PRInfo{}, err
	}
	return PRInfo{Repo: repo, Number: v.Number, Head: v.HeadRefOid, Base: v.BaseRefOid}, nil
}

// repoFromPRURL extracts "owner/name" from a PR url like
// https://github.com/owner/name/pull/123.
func repoFromPRURL(u string) (string, error) {
	i := strings.Index(u, "/pull/")
	if i < 0 {
		return "", fmt.Errorf("cannot parse repo from PR url %q", u)
	}
	rest := strings.TrimRight(u[:i], "/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("cannot parse repo from PR url %q", u)
	}
	owner := parts[len(parts)-2]
	name := parts[len(parts)-1]
	if owner == "" || name == "" {
		return "", fmt.Errorf("cannot parse repo from PR url %q", u)
	}
	return owner + "/" + name, nil
}

func main() {
	cfg := loadConfig()

	var (
		rev       = flag.String("r", cfg.Rev, "revset (jj) or rev (git); default: jj @ (current change) or git working tree")
		output    = flag.String("o", cfg.Output, "output review filename")
		outDir    = flag.String("dir", cfg.Dir, "directory for the output file (e.g. \".claude\")")
		port      = flag.Int("port", cfg.Port, "HTTP port (0 = random)")
		open      = flag.Bool("open", cfg.Open, "open browser")
		editorCmd = flag.String("editor", cfg.Editor, "editor command template; {file} and {line} are substituted (e.g. \"code -g {file}:{line}\")")
		collapse  = flag.Int("collapse", cfg.Collapse, "auto-collapse files with more than N changed lines (0 disables)")
		prArg     = flag.String("pr", cfg.PR, "review a GitHub PR by number or URL (uses the gh CLI)")
		sync      = flag.Bool("sync", cfg.Sync, "one-shot review: block until Submit, print the review to stdout, then exit (no review.md written)")
		md        = flag.String("md", cfg.MD, "review a markdown file as a rendered document (compose with -sync)")
	)
	flag.Parse()
	_ = md

	if *editorCmd == "" {
		if _, err := exec.LookPath("code"); err == nil {
			*editorCmd = "code -g {file}:{line}"
		} else if _, err := exec.LookPath("cursor"); err == nil {
			*editorCmd = "cursor -g {file}:{line}"
		}
	}

	vcs, err := detectVCS()
	if err != nil {
		die("%v", err)
	}

	if *rev == "" {
		if vcs == "jj" {
			*rev = "@"
		}
		// For git, leave rev empty to diff the working tree against HEAD.
	}

	var prInfo *PRInfo
	if *prArg != "" {
		info, err := githubPRInfo(*prArg)
		if err != nil {
			die("%v", err)
		}
		prInfo = &info
	}

	outPath := *output
	if !filepath.IsAbs(outPath) && *outDir != "" {
		outPath = filepath.Join(*outDir, outPath)
	}
	outAbs, err := filepath.Abs(outPath)
	if err != nil {
		die("%v", err)
	}
	if d := filepath.Dir(outAbs); d != "" {
		_ = os.MkdirAll(d, 0755)
	}

	computeData := func() (DiffData, error) {
		var (
			diff           string
			untrackedPaths map[string]bool
		)
		if prInfo != nil {
			d, err := githubPRDiff(*prArg)
			if err != nil {
				return DiffData{}, fmt.Errorf("getting PR diff: %w", err)
			}
			diff = d
			untrackedPaths = map[string]bool{}
		} else {
			d, up, err := getDiff(vcs, *rev)
			if err != nil {
				return DiffData{}, fmt.Errorf("getting diff: %w", err)
			}
			diff, untrackedPaths = d, up
		}
		files, err := parseDiff(diff)
		if err != nil {
			return DiffData{}, fmt.Errorf("parsing diff: %w", err)
		}
		for fi := range files {
			if untrackedPaths[files[fi].Path] {
				files[fi].Untracked = true
			}
			for hi := range files[fi].Hunks {
				annotateIntraLine(&files[fi].Hunks[hi])
				for _, l := range files[fi].Hunks[hi].Lines {
					switch l.Kind {
					case "add":
						files[fi].AddCount++
					case "del":
						files[fi].DelCount++
					}
				}
			}
		}
		priorComments, priorGen := loadPrior(outAbs)
		return DiffData{Rev: *rev, VCS: vcs, Files: files, Prior: priorComments, PriorGen: priorGen, PR: prInfo}, nil
	}

	// Initial compute to surface "no changes" / load errors at startup.
	data, err := computeData()
	if err != nil {
		die("%v", err)
	}
	if len(data.Files) == 0 {
		fmt.Fprintln(os.Stderr, "No changes found yet — server will stay up, reload to check again")
	}
	if len(data.Prior) > 0 || data.PriorGen != "" {
		fmt.Fprintf(os.Stderr, "loaded %d prior comment(s) from %s\n", len(data.Prior), outAbs)
	}
	_ = data

	mux := http.NewServeMux()

	tmpl, err := template.ParseFS(assets, "index.html")
	if err != nil {
		die("parsing template: %v", err)
	}

	repoRoot, _ := repoRootDir(vcs)

	displayHdrRev := *rev
	displayHdrVCS := vcs
	if prInfo != nil {
		displayHdrRev = fmt.Sprintf("PR #%d", prInfo.Number)
		displayHdrVCS = "github"
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, map[string]interface{}{
			"Rev":       displayHdrRev,
			"VCS":       displayHdrVCS,
			"Out":       outAbs,
			"HasEditor": *editorCmd != "",
			"Collapse":  *collapse,
			"Sync":      *sync,
		})
	})

	mux.HandleFunc("/open", func(w http.ResponseWriter, r *http.Request) {
		if *editorCmd == "" {
			http.Error(w, "no editor configured", 400)
			return
		}
		path := r.URL.Query().Get("path")
		line := r.URL.Query().Get("line")
		if path == "" {
			http.Error(w, "missing path", 400)
			return
		}
		if line == "" {
			line = "1"
		}
		abs := path
		if !filepath.IsAbs(abs) && repoRoot != "" {
			abs = filepath.Join(repoRoot, path)
		}
		cmdStr := strings.ReplaceAll(*editorCmd, "{file}", shellQuote(abs))
		cmdStr = strings.ReplaceAll(cmdStr, "{line}", line)
		fmt.Fprintln(os.Stderr, "open:", cmdStr)
		c := exec.Command("sh", "-c", cmdStr)
		c.Stdout = os.Stderr
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		go c.Wait()
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/diff", func(w http.ResponseWriter, r *http.Request) {
		d, err := computeData()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(d)
	})

	doneCh := make(chan struct{}, 1)
	submitCh := make(chan string, 1)

	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		if *sync {
			http.Error(w, "disabled in sync mode; use Submit", 404)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		var req SaveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		md := renderMarkdown(*rev, vcs, "", prInfo, req)
		if err := os.WriteFile(outAbs, []byte(md), 0644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		fmt.Fprintf(w, "wrote %s\n", outAbs)
		select {
		case doneCh <- struct{}{}:
		default:
		}
	})

	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		var req SaveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		md := renderMarkdown(*rev, vcs, "", prInfo, req)
		select {
		case submitCh <- md:
		default: // already submitted; first one wins
		}
		w.Write([]byte("Review submitted — you can close this tab"))
	})

	mux.HandleFunc("/markdown", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		var req SaveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write([]byte(renderMarkdown(*rev, vcs, "", prInfo, req)))
	})

	mux.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
		if *sync {
			http.Error(w, "disabled in sync mode; use Submit", 404)
			return
		}
		w.Write([]byte("bye"))
		go func() {
			time.Sleep(200 * time.Millisecond)
			os.Exit(0)
		}()
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		die("listen: %v", err)
	}
	url := fmt.Sprintf("http://%s", ln.Addr().String())
	// The startup banner is diagnostic, not program output, so it always goes
	// to stderr. This keeps stdout clean in every mode — reserved for the
	// rendered review in sync mode, and empty otherwise.
	infoW := os.Stderr
	fmt.Fprintln(infoW, "gutter:", url)
	if !*sync {
		fmt.Fprintln(infoW, "output:   ", outAbs)
	}
	if prInfo != nil {
		fmt.Fprintln(infoW, "pr:       ", fmt.Sprintf("#%d", prInfo.Number), "("+prInfo.Repo+")")
		fmt.Fprintln(os.Stderr, "note: showing the PR diff; the local working tree is NOT the PR's code")
	} else {
		displayRev := *rev
		if displayRev == "" {
			displayRev = "(working tree)"
		}
		fmt.Fprintln(infoW, "rev:      ", displayRev, "("+vcs+")")
	}
	if *sync {
		fmt.Fprintln(infoW, "sync:      waiting for Submit (no review.md will be written)")
	}

	if *open {
		go openBrowser(url)
	}

	if *sync {
		go func() {
			md := <-submitCh
			time.Sleep(150 * time.Millisecond) // let the HTTP response flush to the browser
			fmt.Print(md)
			os.Exit(0)
		}()
	} else {
		go func() {
			<-doneCh
		}()
	}

	srv := &http.Server{Handler: mux}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		die("serve: %v", err)
	}
}

func detectVCS() (string, error) {
	if err := exec.Command("jj", "root").Run(); err == nil {
		return "jj", nil
	}
	if err := exec.Command("git", "rev-parse", "--git-dir").Run(); err == nil {
		return "git", nil
	}
	return "", fmt.Errorf("not a jj or git repository")
}

var mdRenderer = goldmark.New()

// renderDoc parses a markdown file and returns its top-level blocks, each with
// its rendered HTML fragment and 1-based source line range.
func renderDoc(path string) (Doc, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return Doc{}, err
	}
	lineStarts := computeLineStarts(src)
	root := mdRenderer.Parser().Parse(text.NewReader(src))
	var blocks []DocBlock
	for n := root.FirstChild(); n != nil; n = n.NextSibling() {
		start, end := nodeLineRange(n, lineStarts)
		var buf bytes.Buffer
		if err := mdRenderer.Renderer().Render(&buf, src, n); err != nil {
			return Doc{}, err
		}
		source := ""
		if start >= 1 && start <= len(lineStarts) {
			s := lineStarts[start-1]
			e := len(src)
			if end < len(lineStarts) {
				e = lineStarts[end] // start of the line after `end`
			}
			source = strings.TrimRight(string(src[s:e]), "\n")
		}
		blocks = append(blocks, DocBlock{
			HTML:      buf.String(),
			LineStart: start,
			LineEnd:   end,
			Source:    source,
		})
	}
	return Doc{Path: path, Blocks: blocks}, nil
}

// computeLineStarts returns the byte offset at which each source line begins.
func computeLineStarts(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// offsetToLine maps a byte offset to its 1-based line number.
func offsetToLine(lineStarts []int, off int) int {
	lo, hi := 0, len(lineStarts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if lineStarts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

// nodeLineRange returns the 1-based source line span covered by a block node,
// derived from the text segments of the node and its descendants. Nodes with no
// text segments (e.g. thematic breaks) fall back to line 1.
func nodeLineRange(n ast.Node, lineStarts []int) (int, int) {
	minOff, maxOff := -1, -1
	var visit func(ast.Node)
	visit = func(nd ast.Node) {
		if nd.Type() == ast.TypeBlock {
			if lines := nd.Lines(); lines != nil && lines.Len() > 0 {
				for i := 0; i < lines.Len(); i++ {
					seg := lines.At(i)
					if minOff == -1 || seg.Start < minOff {
						minOff = seg.Start
					}
					if seg.Stop > maxOff {
						maxOff = seg.Stop
					}
				}
			}
		}
		for c := nd.FirstChild(); c != nil; c = c.NextSibling() {
			visit(c)
		}
	}
	visit(n)
	if minOff == -1 {
		return 1, 1
	}
	endOff := maxOff - 1
	if endOff < minOff {
		endOff = minOff
	}
	return offsetToLine(lineStarts, minOff), offsetToLine(lineStarts, endOff)
}

func getDiff(vcs, rev string) (string, map[string]bool, error) {
	var cmd *exec.Cmd
	if vcs == "jj" {
		cmd = exec.Command("jj", "diff", "--git", "-r", rev)
	} else if rev != "" {
		cmd = exec.Command("git", "diff", rev)
	} else {
		cmd = exec.Command("git", "diff")
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", nil, fmt.Errorf("%v: %s", err, errOut.String())
	}
	diff := out.String()
	untrackedPaths := map[string]bool{}

	// For git working-tree mode, append synthetic diffs for untracked files.
	if vcs == "git" && rev == "" {
		extra, paths, err := gitUntrackedDiff()
		if err == nil && extra != "" {
			diff += extra
			untrackedPaths = paths
		}
	}

	return diff, untrackedPaths, nil
}

// gitUntrackedDiff lists untracked files and generates git-style diff output
// so they appear as new files in the review UI.
func gitUntrackedDiff() (string, map[string]bool, error) {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return "", nil, err
	}
	paths := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(paths) == 1 && paths[0] == "" {
		return "", nil, nil
	}
	pathSet := make(map[string]bool, len(paths))
	var b strings.Builder
	for _, p := range paths {
		content, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		pathSet[p] = true
		lines := strings.Split(string(content), "\n")
		// Drop trailing empty line from final newline
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n", p, p)
		fmt.Fprintf(&b, "new file mode 100644\n")
		fmt.Fprintf(&b, "--- /dev/null\n")
		fmt.Fprintf(&b, "+++ b/%s\n", p)
		fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
		for _, ln := range lines {
			fmt.Fprintf(&b, "+%s\n", ln)
		}
	}
	return b.String(), pathSet, nil
}

// githubPRDiff returns the unified git diff for a GitHub PR via the gh CLI.
// arg is a PR number or a full PR URL (gh accepts either).
func githubPRDiff(arg string) (string, error) {
	cmd := exec.Command("gh", "pr", "diff", arg)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh pr diff %s: %v: %s", arg, err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// githubPRInfo fetches PR metadata via the gh CLI.
func githubPRInfo(arg string) (PRInfo, error) {
	cmd := exec.Command("gh", "pr", "view", arg, "--json", "number,headRefOid,baseRefOid,url")
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return PRInfo{}, fmt.Errorf("gh pr view %s: %v: %s", arg, err, strings.TrimSpace(errOut.String()))
	}
	return parsePRView(out.Bytes())
}

func parseDiff(s string) ([]File, error) {
	var files []File
	var cur *File
	var hunk *Hunk
	oldLn, newLn := 0, 0

	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		switch {
		case strings.HasPrefix(ln, "diff --git"):
			if cur != nil {
				if hunk != nil {
					cur.Hunks = append(cur.Hunks, *hunk)
					hunk = nil
				}
				files = append(files, *cur)
			}
			path := parseGitDiffPath(ln)
			cur = &File{Path: path, Lang: langFor(path)}
		case strings.HasPrefix(ln, "+++ "):
			if cur != nil {
				p := strings.TrimPrefix(ln, "+++ ")
				if strings.HasPrefix(p, "b/") {
					p = p[2:]
				}
				if p != "/dev/null" {
					cur.Path = p
					cur.Lang = langFor(p)
				}
			}
		case strings.HasPrefix(ln, "@@"):
			if hunk != nil && cur != nil {
				cur.Hunks = append(cur.Hunks, *hunk)
			}
			h := parseHunkHeader(ln)
			hunk = &h
			oldLn = h.OldStart
			newLn = h.NewStart
		case hunk != nil && cur != nil && len(ln) > 0:
			switch ln[0] {
			case '+':
				hunk.Lines = append(hunk.Lines, Line{Kind: "add", NewLine: newLn, Text: ln[1:]})
				newLn++
			case '-':
				hunk.Lines = append(hunk.Lines, Line{Kind: "del", OldLine: oldLn, Text: ln[1:]})
				oldLn++
			case ' ':
				hunk.Lines = append(hunk.Lines, Line{Kind: "ctx", OldLine: oldLn, NewLine: newLn, Text: ln[1:]})
				oldLn++
				newLn++
			case '\\':
				// "\ No newline at end of file"
			}
		}
	}
	if hunk != nil && cur != nil {
		cur.Hunks = append(cur.Hunks, *hunk)
	}
	if cur != nil {
		files = append(files, *cur)
	}
	return files, nil
}

func parseGitDiffPath(ln string) string {
	// "diff --git a/path b/path"
	parts := strings.SplitN(ln, " b/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ln
}

func parseHunkHeader(ln string) Hunk {
	h := Hunk{Header: ln}
	// @@ -a,b +c,d @@
	fmt.Sscanf(ln, "@@ -%d,%d +%d,%d @@", &h.OldStart, new(int), &h.NewStart, new(int))
	if h.NewStart == 0 {
		// try single-line form: @@ -a +c @@
		fmt.Sscanf(ln, "@@ -%d +%d @@", &h.OldStart, &h.NewStart)
	}
	return h
}

func renderMarkdown(rev, vcs, docPath string, pr *PRInfo, req SaveRequest) string {
	var b strings.Builder
	if docPath != "" {
		fmt.Fprintf(&b, "# Review of %s\n\n", docPath)
	} else if pr != nil {
		fmt.Fprintf(&b, "# Review of PR #%d (github)\n\n", pr.Number)
		fmt.Fprintf(&b, "## PR\n\n")
		fmt.Fprintf(&b, "- repo: %s\n", pr.Repo)
		fmt.Fprintf(&b, "- number: %d\n", pr.Number)
		fmt.Fprintf(&b, "- head: %s\n", pr.Head)
		fmt.Fprintf(&b, "- base: %s\n\n", pr.Base)
		fmt.Fprintf(&b, "NOTE: This is a GitHub PR review. The local working tree is NOT the PR's code —\n")
		fmt.Fprintf(&b, "do not read local files to understand the changes. Use `gh pr diff %d` (or\n", pr.Number)
		fmt.Fprintf(&b, "`gh pr view %d`) to see the actual changes these comments refer to.\n", pr.Number)
		fmt.Fprintf(&b, "To post a comment: `gh` review-comment API — use `head` as the commit id,\n")
		fmt.Fprintf(&b, "`path`/`line` from each comment, side RIGHT for added/context, LEFT for removed.\n\n")
	} else {
		fmt.Fprintf(&b, "# Review of `%s` (%s)\n\n", rev, vcs)
	}
	if strings.TrimSpace(req.General) != "" {
		b.WriteString("## General feedback\n\n")
		b.WriteString(strings.TrimSpace(req.General))
		b.WriteString("\n\n")
	}
	if len(req.Comments) > 0 {
		b.WriteString("## Inline comments\n\n")
		for _, c := range req.Comments {
			loc := fmt.Sprintf("%s:%d", c.Path, c.Line)
			if c.EndLine != 0 && c.EndLine != c.Line {
				loc = fmt.Sprintf("%s:%d-%d", c.Path, c.Line, c.EndLine)
			}
			if pr != nil && c.Side == "old" {
				loc += " (LEFT)"
			}
			fmt.Fprintf(&b, "### %s\n\n", loc)
			if strings.TrimSpace(c.Snippet) != "" {
				b.WriteString("```\n")
				b.WriteString(c.Snippet)
				if !strings.HasSuffix(c.Snippet, "\n") {
					b.WriteString("\n")
				}
				b.WriteString("```\n\n")
			}
			b.WriteString(strings.TrimSpace(c.Body))
			b.WriteString("\n\n")
		}
	}
	if req.General == "" && len(req.Comments) == 0 {
		b.WriteString("_(no feedback)_\n")
	}
	return b.String()
}

var inlineHeaderRe = regexp.MustCompile(`^###\s+(.+?):(\d+)(?:-(\d+))?(?:\s+\((LEFT)\))?\s*$`)
var tokenRe = regexp.MustCompile(`[A-Za-z_][A-Za-z_0-9]*|\s+|.`)

func tokenize(s string) []string {
	return tokenRe.FindAllString(s, -1)
}

// annotateIntraLine pairs contiguous del→add blocks within a hunk and
// computes word-level segments for each paired line.
func annotateIntraLine(h *Hunk) {
	i := 0
	for i < len(h.Lines) {
		if h.Lines[i].Kind != "del" {
			i++
			continue
		}
		delStart := i
		for i < len(h.Lines) && h.Lines[i].Kind == "del" {
			i++
		}
		addStart := i
		for i < len(h.Lines) && h.Lines[i].Kind == "add" {
			i++
		}
		nDel := addStart - delStart
		nAdd := i - addStart
		pairs := nDel
		if nAdd < pairs {
			pairs = nAdd
		}
		for k := 0; k < pairs; k++ {
			delSegs, addSegs, ratio := wordDiff(h.Lines[delStart+k].Text, h.Lines[addStart+k].Text)
			// Only apply if the lines share enough — otherwise it's noise.
			if ratio >= 0.4 {
				h.Lines[delStart+k].Segments = delSegs
				h.Lines[addStart+k].Segments = addSegs
			}
		}
	}
}

func wordDiff(a, b string) (delSegs, addSegs []Segment, ratio float64) {
	at := tokenize(a)
	bt := tokenize(b)
	m, n := len(at), len(bt)
	if m == 0 && n == 0 {
		return nil, nil, 1
	}

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if at[i-1] == bt[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	type op struct{ kind, text, side string }
	ops := make([]op, 0, m+n)
	i, j := m, n
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && at[i-1] == bt[j-1]:
			ops = append(ops, op{"same", at[i-1], "both"})
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			ops = append(ops, op{"ins", bt[j-1], "add"})
			j--
		default:
			ops = append(ops, op{"del", at[i-1], "del"})
			i--
		}
	}
	// reverse
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}

	commonChars := 0
	totalChars := 0
	for _, o := range ops {
		switch o.side {
		case "both":
			commonChars += len(o.text)
			totalChars += len(o.text)
			appendSeg(&delSegs, "same", o.text)
			appendSeg(&addSegs, "same", o.text)
		case "add":
			totalChars += len(o.text)
			appendSeg(&addSegs, "ins", o.text)
		case "del":
			totalChars += len(o.text)
			appendSeg(&delSegs, "del", o.text)
		}
	}
	if totalChars == 0 {
		return nil, nil, 1
	}
	ratio = float64(commonChars) / float64(totalChars)
	return
}

func appendSeg(segs *[]Segment, kind, text string) {
	if len(*segs) > 0 && (*segs)[len(*segs)-1].Kind == kind {
		(*segs)[len(*segs)-1].Text += text
		return
	}
	*segs = append(*segs, Segment{Kind: kind, Text: text})
}

// loadPrior parses a previously-saved review.md. Tolerant of light edits.
func loadPrior(path string) ([]Comment, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	lines := strings.Split(string(b), "\n")

	var (
		comments  []Comment
		general   strings.Builder
		section   string // "", "general", "inline"
		cur       *Comment
		curBody   strings.Builder
		curSnip   strings.Builder
		inSnippet bool
	)

	flushCur := func() {
		if cur == nil {
			return
		}
		cur.Body = strings.TrimSpace(curBody.String())
		cur.Snippet = strings.TrimRight(curSnip.String(), "\n")
		if cur.Body != "" {
			comments = append(comments, *cur)
		}
		cur = nil
		curBody.Reset()
		curSnip.Reset()
		inSnippet = false
	}

	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "## PR"):
			// Forge metadata block (PR review); intentionally ignored on read.
			continue
		case strings.HasPrefix(ln, "## General feedback"):
			flushCur()
			section = "general"
			continue
		case strings.HasPrefix(ln, "## Inline comments"):
			flushCur()
			section = "inline"
			continue
		case strings.HasPrefix(ln, "# "):
			continue
		}

		if section == "general" && !strings.HasPrefix(ln, "## ") && !strings.HasPrefix(ln, "### ") {
			general.WriteString(ln)
			general.WriteString("\n")
		}

		if section == "inline" {
			if m := inlineHeaderRe.FindStringSubmatch(ln); m != nil {
				flushCur()
				start, _ := strconv.Atoi(m[2])
				end := start
				if m[3] != "" {
					end, _ = strconv.Atoi(m[3])
				}
				side := "new"
				if m[4] == "LEFT" {
					side = "old"
				}
				cur = &Comment{Path: m[1], Side: side, Line: start, EndLine: end}
				continue
			}
			if cur != nil {
				if strings.TrimSpace(ln) == "```" {
					inSnippet = !inSnippet
					continue
				}
				if inSnippet {
					curSnip.WriteString(ln)
					curSnip.WriteString("\n")
				} else {
					curBody.WriteString(ln)
					curBody.WriteString("\n")
				}
			}
		}
	}
	flushCur()

	return comments, strings.TrimSpace(general.String())
}

func openBrowser(url string) {
	exec.Command("xdg-open", url).Start()
}

func repoRootDir(vcs string) (string, error) {
	var cmd *exec.Cmd
	if vcs == "jj" {
		cmd = exec.Command("jj", "root")
	} else {
		cmd = exec.Command("git", "rev-parse", "--show-toplevel")
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func die(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "gutter: "+format+"\n", a...)
	os.Exit(1)
}
