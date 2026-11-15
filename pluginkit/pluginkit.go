// Package pluginkit helps write gpt-home plugins in Go.
//
// A plugin is an executable that the hub starts with the argument "serve". It speaks MCP over stdio (newline-delimited
// JSON-RPC on stdin and stdout) and writes its log to stderr. See docs/PLUGINS.md for the full contract.
//
// A minimal plugin:
//
//	s := pluginkit.New(pluginkit.Plugin{ID: "echo", Name: "gpt-echo", Title: "Echo", Version: "0.1.0"})
//	mcp.AddTool(s, &mcp.Tool{Name: "echo_say", Description: "Repeats the text back."}, say)
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer stop()
//	if err := pluginkit.Serve(ctx, s); err != nil {
//		log.Fatal(err)
//	}
//
// The tool helpers ([Admin], [Timeout], [Inventory]) set the tool _meta keys the hub understands and return the same
// tool, so they chain: pluginkit.Admin(pluginkit.Timeout(tool, 3*time.Minute)).
package pluginkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool _meta keys and request _meta keys of the plugin protocol.
const (
	MetaAudience = "gpt-home/audience" // "admin" hides a tool from assistants; the default is "assistant"
	MetaTimeout  = "gpt-home/timeout"  // the hub-side call timeout in seconds
	MetaRole     = "gpt-home/role"     // "inventory" marks the device-list tool
	MetaCaller   = "gpt-home/caller"   // request _meta: the person's display name
	MetaVia      = "gpt-home/via"      // request _meta: "site", "mcp", "admin" or "cli"
)

// Environment variables the hub sets for a plugin.
const (
	EnvPluginID   = "GPT_HOME_PLUGIN_ID"
	EnvDataDir    = "GPT_HOME_DATA_DIR"
	EnvLogLevel   = "GPT_HOME_LOG_LEVEL"
	EnvHubVersion = "GPT_HOME_HUB_VERSION"
)

// Plugin is the identity a plugin reports in its MCP initialize result.
type Plugin struct {
	ID           string // "tv"; every tool name starts with "<ID>_"
	Name         string // "gpt-tv" (serverInfo.name)
	Title        string // "TV" (serverInfo.title)
	Version      string // semver
	Instructions string // optional short English guidance for the model
}

var (
	pluginIDRe = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)
	verbRe     = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// New returns a go-sdk server with the plugin's identity and instructions set. Its log goes to stderr at the level of
// GPT_HOME_LOG_LEVEL (default info). It panics when p.ID breaks the plugin id rule, like mcp.AddTool panics on a bad
// tool: both are programming errors.
func New(p Plugin) *mcp.Server {
	if err := checkPluginID(p.ID); err != nil {
		panic(err)
	}
	impl := &mcp.Implementation{Name: p.Name, Title: p.Title, Version: p.Version}
	return mcp.NewServer(impl, &mcp.ServerOptions{
		Instructions: p.Instructions,
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()})),
	})
}

// logLevel returns the level the hub asks for in GPT_HOME_LOG_LEVEL (debug, info, warn or error); info by default.
func logLevel() slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(os.Getenv(EnvLogLevel))); err != nil {
		return slog.LevelInfo
	}
	return l
}

// Admin hides t from assistants: only the admin UI and the homed CLI see it.
func Admin(t *mcp.Tool) *mcp.Tool {
	return setMeta(t, MetaAudience, "admin")
}

// Timeout sets the hub-side call timeout of t, in whole seconds rounded up. The hub default is 30 s and its maximum
// is 300 s.
func Timeout(t *mcp.Tool, d time.Duration) *mcp.Tool {
	return setMeta(t, MetaTimeout, int(math.Ceil(d.Seconds())))
}

// Inventory marks t as the plugin's device list. Use it on at most one read-only tool; it must return
// {"items":[{"id","name","subtitle"?,"state"?}]} with state one of on|off|offline|unknown|paired|unpaired.
func Inventory(t *mcp.Tool) *mcp.Tool {
	return setMeta(t, MetaRole, "inventory")
}

func setMeta(t *mcp.Tool, key string, value any) *mcp.Tool {
	if t.Meta == nil {
		t.Meta = mcp.Meta{}
	}
	t.Meta[key] = value
	return t
}

// DataDir returns the plugin's persistent data directory: GPT_HOME_DATA_DIR when the hub runs the plugin, otherwise
// ./data for development. The directory is created with mode 0700 if it is missing.
func DataDir() (string, error) {
	dir := os.Getenv(EnvDataDir)
	if dir == "" {
		dir = "data"
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// Caller returns who asked for this call, from the request _meta the hub adds. Both are empty when the call did not
// come through the hub. Log them if useful, but never trust them for security.
func Caller(req *mcp.CallToolRequest) (person, via string) {
	if req == nil || req.Params == nil {
		return "", ""
	}
	m := req.Params.GetMeta()
	person, _ = m[MetaCaller].(string)
	via, _ = m[MetaVia].(string)
	return person, via
}

// Serve runs s over stdio until ctx is done or stdin is closed. Both are a normal shutdown and return nil.
func Serve(ctx context.Context, s *mcp.Server) error {
	return serve(ctx, s, &mcp.StdioTransport{})
}

// serve is mcp.Server.Run without its error log on a normal shutdown.
func serve(ctx context.Context, s *mcp.Server, t mcp.Transport) error {
	ss, err := s.Connect(ctx, t, nil)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- ss.Wait() }()
	select {
	case <-ctx.Done():
		_ = ss.Close()
		<-done
		return nil
	case err := <-done:
		if errors.Is(err, io.EOF) || errors.Is(err, mcp.ErrConnectionClosed) {
			return nil
		}
		return err
	}
}

// CheckToolName reports whether tool is a valid name for a tool of plugin pluginID: "<pluginID>_<verb>", where the
// plugin id matches ^[a-z][a-z0-9]{1,15}$ and the verb ^[a-z][a-z0-9_]*$. The hub skips tools that fail it.
func CheckToolName(pluginID, tool string) error {
	if err := checkPluginID(pluginID); err != nil {
		return err
	}
	verb, ok := strings.CutPrefix(tool, pluginID+"_")
	if !ok || !verbRe.MatchString(verb) {
		return fmt.Errorf("tool name %q must match ^%s_[a-z][a-z0-9_]*$", tool, pluginID)
	}
	return nil
}

func checkPluginID(id string) error {
	if !pluginIDRe.MatchString(id) {
		return fmt.Errorf("plugin id %q must match %s", id, pluginIDRe)
	}
	return nil
}
