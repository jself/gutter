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
}

type File struct {
	Path     string `json:"path"`
	Lang     string `json:"lang"`
	Hunks    []Hunk `json:"hunks"`
	AddCount int    `json:"add_count"`
	DelCount int    `json:"del_count"`
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

func main() {
	cfg := loadConfig()

	var (
		rev       = flag.String("r", cfg.Rev, "revset (jj) or rev (git); default: jj @ (current change) or git @{u} (vs upstream)")
		output    = flag.String("o", cfg.Output, "output review filename")
		outDir    = flag.String("dir", cfg.Dir, "directory for the output file (e.g. \".claude\")")
		port      = flag.Int("port", cfg.Port, "HTTP port (0 = random)")
		open      = flag.Bool("open", cfg.Open, "open browser")
		editorCmd = flag.String("editor", cfg.Editor, "editor command template; {file} and {line} are substituted (e.g. \"code -g {file}:{line}\")")
		collapse  = flag.Int("collapse", cfg.Collapse, "auto-collapse files with more than N changed lines (0 disables)")
	)
	flag.Parse()

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
		} else {
			*rev = "@{u}"
		}
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
		diff, err := getDiff(vcs, *rev)
		if err != nil {
			return DiffData{}, fmt.Errorf("getting diff: %w", err)
		}
		files, err := parseDiff(diff)
		if err != nil {
			return DiffData{}, fmt.Errorf("parsing diff: %w", err)
		}
		for fi := range files {
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
		return DiffData{Rev: *rev, VCS: vcs, Files: files, Prior: priorComments, PriorGen: priorGen}, nil
	}

	// Initial compute to surface "no changes" / load errors at startup.
	data, err := computeData()
	if err != nil {
		die("%v", err)
	}
	if len(data.Files) == 0 {
		fmt.Fprintln(os.Stderr, "No changes found for", *rev)
		os.Exit(0)
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

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, map[string]interface{}{
			"Rev":       *rev,
			"VCS":       vcs,
			"Out":       outAbs,
			"HasEditor": *editorCmd != "",
			"Collapse":  *collapse,
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

	mux.HandleFunc("/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", 405)
			return
		}
		var req SaveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		md := renderMarkdown(*rev, vcs, req)
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
		w.Write([]byte(renderMarkdown(*rev, vcs, req)))
	})

	mux.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
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
	fmt.Println("gutter:", url)
	fmt.Println("output:   ", outAbs)
	fmt.Println("rev:      ", *rev, "("+vcs+")")

	if *open {
		go openBrowser(url)
	}

	go func() {
		<-doneCh
	}()

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

func getDiff(vcs, rev string) (string, error) {
	var cmd *exec.Cmd
	if vcs == "jj" {
		cmd = exec.Command("jj", "diff", "--git", "-r", rev)
	} else {
		cmd = exec.Command("git", "diff", rev)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, errOut.String())
	}
	return out.String(), nil
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

func renderMarkdown(rev, vcs string, req SaveRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Review of `%s` (%s)\n\n", rev, vcs)
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

var inlineHeaderRe = regexp.MustCompile(`^###\s+(.+?):(\d+)(?:-(\d+))?\s*$`)
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
				cur = &Comment{Path: m[1], Side: "new", Line: start, EndLine: end}
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
