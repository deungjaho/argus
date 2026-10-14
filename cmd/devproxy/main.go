// devproxy — deterministic worktree ports + unified *.dev.localhost routing
// (argus#17). Independent failure domain from beacon/argus: pure port routing.
//
//	devproxy port [path]            print the deterministic port for a worktree
//	devproxy register [path]        record slug→port mapping for the router
//	devproxy unregister [path]
//	devproxy list                   show registered routes
//	devproxy serve                  run the reverse proxy on --listen (default :7999)
//
// Deterministic port: base + fnv32(abs path) % range. Same worktree always gets
// the same port — no negotiation, freed when the worktree is deleted.
// Routing: <slug>.dev.localhost:7999 → 127.0.0.1:<port>. The `localhost` TLD
// resolves 127.0.0.1 natively; no /etc/hosts edits needed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	basePort = 3000
	portSpan = 4000
	listen   = ":7999"
	domain   = "dev.localhost"
)

func registryPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "devproxy", "routes.json")
}

type route struct {
	Slug string `json:"slug"`
	Path string `json:"path"`
	Port int    `json:"port"`
}

func loadRoutes() map[string]route {
	m := map[string]route{}
	data, err := os.ReadFile(registryPath())
	if err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func saveRoutes(m map[string]route) {
	_ = os.MkdirAll(filepath.Dir(registryPath()), 0o755)
	data, _ := json.MarshalIndent(m, "", "  ")
	tmp := registryPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err == nil {
		_ = os.Rename(tmp, registryPath())
	}
}

// portFor deterministically maps a worktree path to a port.
func portFor(absPath string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(absPath))
	return basePort + int(h.Sum32()%uint32(portSpan))
}

var nonDNS = regexp.MustCompile(`[^a-z0-9-]+`)

// slugFor maps a worktree path to a DNS-safe subdomain label.
func slugFor(absPath string) string {
	slug := nonDNS.ReplaceAllString(strings.ToLower(filepath.Base(absPath)), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "root"
	}
	return slug
}

func absPath(p string) string {
	if p == "" {
		p = "."
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

func main() {
	flag.IntVar(&basePort, "base", basePort, "lowest allocatable port")
	flag.IntVar(&portSpan, "range", portSpan, "port allocation range size")
	flag.StringVar(&listen, "listen", listen, "proxy listen address")
	flag.StringVar(&domain, "domain", domain, "routing domain suffix")
	flag.Parse()

	args := flag.Args()
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "port":
		p := "."
		if len(args) > 0 {
			p = args[0]
		}
		fmt.Println(portFor(absPath(p)))

	case "register":
		p := "."
		if len(args) > 0 {
			p = args[0]
		}
		abs := absPath(p)
		r := route{Slug: slugFor(abs), Path: abs, Port: portFor(abs)}
		m := loadRoutes()
		if old, ok := m[r.Slug]; ok && old.Path != r.Path {
			fmt.Fprintf(os.Stderr, "devproxy: slug %q already routes to %s — pick a distinct directory name\n", r.Slug, old.Path)
			os.Exit(1)
		}
		for slug, old := range m {
			if slug != r.Slug && old.Port == r.Port {
				fmt.Fprintf(os.Stderr, "devproxy: warning: port %d shared with %s (%s.dev.localhost)\n", r.Port, old.Path, slug)
			}
		}
		m[r.Slug] = r
		saveRoutes(m)
		pushBeacon(r)
		fmt.Printf("%s.%s -> 127.0.0.1:%d  (%s)\n", r.Slug, domain, r.Port, r.Path)

	case "unregister":
		abs := absPath(firstOr(args, "."))
		m := loadRoutes()
		delete(m, slugFor(abs))
		saveRoutes(m)
		unpushBeacon(abs)

	case "list":
		m := loadRoutes()
		for slug, r := range m {
			fmt.Printf("%s.%s -> 127.0.0.1:%d  (%s)\n", slug, domain, r.Port, r.Path)
		}

	case "serve":
		serve()

	default:
		fmt.Fprintf(os.Stderr, "usage: devproxy [port|register|unregister|list|serve] [path]\n")
		os.Exit(2)
	}
}

func firstOr(s []string, d string) string {
	if len(s) == 0 {
		return d
	}
	return s[0]
}

// pushBeacon reports the worktree to beacon's resource registry. Best-effort:
// beacon may be absent — routing must never depend on it.
func pushBeacon(r route) {
	beacon, err := exec.LookPath("beacon")
	if err != nil {
		return
	}
	branch, _ := exec.Command("git", "-C", r.Path, "rev-parse", "--abbrev-ref", "HEAD").Output()
	common, _ := exec.Command("git", "-C", r.Path, "rev-parse", "--git-common-dir").Output()
	repo := ""
	if c := strings.TrimSpace(string(common)); c != "" {
		if !filepath.IsAbs(c) {
			c = filepath.Join(r.Path, c) // ".git" in the main checkout
		}
		repo = filepath.Base(filepath.Dir(c))
	}
	meta, _ := json.Marshal(map[string]any{
		"path":   r.Path,
		"port":   r.Port,
		"slug":   r.Slug,
		"branch": strings.TrimSpace(string(branch)),
		"repo":   repo,
	})
	args := []string{"register", "worktree", r.Path, "--producer", "devproxy", "--data", string(meta)}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		args = append(args, "--pane", pane)
	}
	_ = exec.Command(beacon, args...).Run()
}

func unpushBeacon(abs string) {
	if beacon, err := exec.LookPath("beacon"); err == nil {
		_ = exec.Command(beacon, "unregister", "worktree", abs).Run()
	}
}

func serve() {
	suffix := "." + domain
	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		host := req.Host
		if i := strings.Index(host, ":"); i >= 0 {
			host = host[:i]
		}
		if !strings.HasSuffix(host, suffix) {
			http.Error(w, fmt.Sprintf("devproxy: want <slug>%s host, got %q", suffix, host), http.StatusBadRequest)
			return
		}
		slug := strings.TrimSuffix(host, suffix)
		r, ok := loadRoutes()[slug]
		if !ok {
			http.Error(w, fmt.Sprintf("devproxy: no route for slug %q (devproxy register <path>)", slug), http.StatusBadGateway)
			return
		}
		target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", r.Port)}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.FlushInterval = 50 * time.Millisecond
		proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
			http.Error(w, fmt.Sprintf("devproxy: upstream 127.0.0.1:%d unreachable (%v)", r.Port, err), http.StatusBadGateway)
		}
		// Dev servers bind/validate Host — rewrite to the real target host.
		req.Host = target.Host
		proxy.ServeHTTP(w, req)
	})
	fmt.Printf("devproxy: *.%s -> worktree ports on %s (registry %s)\n", domain, listen, registryPath())
	if err := http.ListenAndServe(listen, nil); err != nil {
		fmt.Fprintln(os.Stderr, "devproxy:", err)
		os.Exit(1)
	}
}
