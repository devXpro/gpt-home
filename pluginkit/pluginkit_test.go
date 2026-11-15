package pluginkit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCheckToolName(t *testing.T) {
	tests := []struct {
		plugin, tool string
		ok           bool
	}{
		{"tv", "tv_power_on", true},
		{"tv", "tv_x", true},
		{"tv", "tv_set_input2", true},
		{"kettle", "kettle_boil", true},
		{"tv", "tv_", false},
		{"tv", "tv_Power", false},
		{"tv", "tv_2on", false},
		{"tv", "tv-power", false},
		{"tv", "tvpower", false},
		{"tv", "kettle_boil", false},
		{"tv", "tv_power-on", false},
		{"t", "t_on", false},                                 // id too short
		{"Tv", "Tv_on", false},                               // id upper case
		{"1tv", "1tv_on", false},                             // id starts with a digit
		{"abcdefghijklmnopq", "abcdefghijklmnopq_on", false}, // id longer than 16
		{"abcdefghijklmnop", "abcdefghijklmnop_on", true},    // id of 16
		{"tv_x", "tv_x_on", false},                           // underscore in id
		{"tv", "", false},
	}
	for _, tt := range tests {
		err := CheckToolName(tt.plugin, tt.tool)
		if (err == nil) != tt.ok {
			t.Errorf("CheckToolName(%q, %q) = %v, want ok=%v", tt.plugin, tt.tool, err, tt.ok)
		}
	}
}

func TestToolHelpersChain(t *testing.T) {
	tool := &mcp.Tool{Name: "tv_pair"}
	got := Admin(Timeout(Inventory(tool), 2500*time.Millisecond))
	if got != tool {
		t.Fatal("helpers must return the same tool")
	}
	want := map[string]any{MetaAudience: "admin", MetaTimeout: 3, MetaRole: "inventory"}
	for k, v := range want {
		if tool.Meta[k] != v {
			t.Errorf("Meta[%q] = %#v, want %#v", k, tool.Meta[k], v)
		}
	}
}

func TestNewPanicsOnBadID(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New with a bad id did not panic")
		}
	}()
	New(Plugin{ID: "Bad-ID", Name: "x", Version: "1"})
}

func TestDataDir(t *testing.T) {
	t.Run("from env", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "plugin", "data")
		t.Setenv(EnvDataDir, dir)
		got, err := DataDir()
		if err != nil || got != dir {
			t.Fatalf("DataDir() = %q, %v; want %q", got, err, dir)
		}
		checkDir(t, dir)
	})
	t.Run("development", func(t *testing.T) {
		t.Setenv(EnvDataDir, "")
		wd := t.TempDir()
		t.Chdir(wd)
		got, err := DataDir()
		if err != nil {
			t.Fatal(err)
		}
		// t.TempDir may sit behind a symlink (macOS /var → /private/var), so compare resolved paths.
		want, _ := filepath.EvalSymlinks(filepath.Join(wd, "data"))
		if resolved, _ := filepath.EvalSymlinks(got); resolved != want {
			t.Fatalf("DataDir() = %q, want %q", got, want)
		}
		checkDir(t, got)
	})
}

func checkDir(t *testing.T, dir string) {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("%s: mode %v, want a 0700 directory", dir, fi.Mode())
	}
}

func TestCallerNil(t *testing.T) {
	if p, v := Caller(nil); p != "" || v != "" {
		t.Fatalf("Caller(nil) = %q, %q", p, v)
	}
	if p, v := Caller(&mcp.CallToolRequest{}); p != "" || v != "" {
		t.Fatalf("Caller(empty) = %q, %q", p, v)
	}
}

// TestServerOverMCP runs a pluginkit server against a go-sdk client in memory: identity, instructions, tool _meta
// and the caller's request _meta must all survive the protocol.
func TestServerOverMCP(t *testing.T) {
	s := New(Plugin{ID: "demo", Name: "gpt-demo", Title: "Demo", Version: "1.2.3", Instructions: "Be brief."})
	type in struct{}
	type out struct {
		Person string `json:"person"`
		Via    string `json:"via"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "demo_who"}, func(_ context.Context, req *mcp.CallToolRequest, _ in) (*mcp.CallToolResult, out, error) {
		p, v := Caller(req)
		return nil, out{Person: p, Via: v}, nil
	})
	mcp.AddTool(s, Admin(Timeout(&mcp.Tool{Name: "demo_pair"}, 3*time.Minute)), func(context.Context, *mcp.CallToolRequest, in) (*mcp.CallToolResult, any, error) {
		return nil, nil, nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	served := make(chan error, 1)
	go func() { served <- serve(ctx, s, st) }()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	init := cs.InitializeResult()
	if si := init.ServerInfo; si == nil || si.Name != "gpt-demo" || si.Title != "Demo" || si.Version != "1.2.3" {
		t.Fatalf("serverInfo = %+v", init.ServerInfo)
	}
	if init.Instructions != "Be brief." {
		t.Fatalf("instructions = %q", init.Instructions)
	}

	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]mcp.Meta{}
	for _, tool := range list.Tools {
		meta[tool.Name] = tool.Meta
	}
	if m := meta["demo_pair"]; m[MetaAudience] != "admin" || m[MetaTimeout] != float64(180) {
		t.Fatalf("demo_pair _meta = %v", m)
	}
	if m := meta["demo_who"]; m[MetaAudience] != nil {
		t.Fatalf("demo_who _meta = %v", m)
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "demo_who",
		Meta: mcp.Meta{MetaCaller: "Roman", MetaVia: "site"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := res.StructuredContent.(map[string]any)
	if got["person"] != "Roman" || got["via"] != "site" {
		t.Fatalf("structured = %v", res.StructuredContent)
	}

	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve after cancel = %v, want nil", err)
	}
}

func TestServeReturnsNilWhenInputCloses(t *testing.T) {
	s := New(Plugin{ID: "demo", Name: "gpt-demo", Version: "1"})
	st, ct := mcp.NewInMemoryTransports()
	served := make(chan error, 1)
	go func() { served <- serve(t.Context(), s, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = cs.Close()
	if err := <-served; err != nil {
		t.Fatalf("serve after the client left = %v, want nil", err)
	}
}
