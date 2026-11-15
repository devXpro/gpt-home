package plugins

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devXpro/gpt-home/internal/hub"
	"github.com/devXpro/gpt-home/pluginkit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestLogRing(t *testing.T) {
	s := newLogSink(filepath.Join(t.TempDir(), "x.log"), slog.New(slog.DiscardHandler))
	defer s.close()
	for i := range logRingLines + 5 {
		s.add(fmt.Sprint("line ", i))
	}
	lines := s.lines()
	if len(lines) != logRingLines || lines[0] != "line 5" || lines[len(lines)-1] != fmt.Sprint("line ", logRingLines+4) {
		t.Fatalf("ring: %d lines, first %q, last %q", len(lines), lines[0], lines[len(lines)-1])
	}
}

func TestLogRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	s := newLogSink(path, slog.New(slog.DiscardHandler))
	s.maxSize = 100
	for i := range 40 {
		s.add(fmt.Sprintf("line %02d ......", i)) // 16 bytes with the newline
	}
	s.close()
	for _, p := range []string{path, path + ".1", path + ".2"} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Size() > 100 || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: size %d, mode %v", p, fi.Size(), fi.Mode().Perm())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Errorf("%s.3 exists: %v", path, err)
	}
	b, _ := os.ReadFile(path) //nolint:gosec // a path inside t.TempDir
	if !strings.HasSuffix(string(b), "line 39 ......\n") {
		t.Errorf("current file ends with %q", b)
	}
}

func TestLogCopyTruncatesLongLines(t *testing.T) {
	s := newLogSink(filepath.Join(t.TempDir(), "x.log"), slog.New(slog.DiscardHandler))
	defer s.close()
	long := strings.Repeat("a", 3*logMaxLine)
	s.copy(strings.NewReader("first\r\n" + long + "\nafter\nno newline at end"))
	lines := s.lines()
	if len(lines) != 4 || lines[0] != "first" || lines[2] != "after" || lines[3] != "no newline at end" {
		t.Fatalf("lines = %d: %.40q", len(lines), lines)
	}
	if !strings.HasSuffix(lines[1], "[truncated]") || len(lines[1]) > logMaxLine+20 {
		t.Errorf("long line kept %d bytes", len(lines[1]))
	}
}

func TestConvertTool(t *testing.T) {
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	tests := []struct {
		name      string
		tool      *mcp.Tool
		skip      bool
		audience  hub.Audience
		timeout   time.Duration
		inventory bool
		warnings  int
	}{
		{name: "plain", tool: &mcp.Tool{Name: "tv_on"}, audience: hub.AudienceAssistant, timeout: 30 * time.Second},
		{name: "bad name", tool: &mcp.Tool{Name: "kettle_on"}, skip: true},
		{name: "admin", tool: pluginkit.Admin(&mcp.Tool{Name: "tv_pair"}), audience: hub.AudienceAdmin, timeout: 30 * time.Second},
		{name: "explicit assistant", tool: &mcp.Tool{Name: "tv_on", Meta: mcp.Meta{pluginkit.MetaAudience: "assistant"}},
			audience: hub.AudienceAssistant, timeout: 30 * time.Second},
		{name: "unknown audience", tool: &mcp.Tool{Name: "tv_on", Meta: mcp.Meta{pluginkit.MetaAudience: 1.0}},
			audience: hub.AudienceAdmin, timeout: 30 * time.Second, warnings: 1},
		{name: "timeout", tool: &mcp.Tool{Name: "tv_pair", Meta: mcp.Meta{pluginkit.MetaTimeout: 180.0}},
			audience: hub.AudienceAssistant, timeout: 180 * time.Second},
		{name: "fractional timeout", tool: &mcp.Tool{Name: "tv_x", Meta: mcp.Meta{pluginkit.MetaTimeout: 1.5}},
			audience: hub.AudienceAssistant, timeout: 1500 * time.Millisecond},
		{name: "huge timeout", tool: &mcp.Tool{Name: "tv_x", Meta: mcp.Meta{pluginkit.MetaTimeout: 1e300}},
			audience: hub.AudienceAssistant, timeout: 300 * time.Second, warnings: 1},
		{name: "zero timeout", tool: &mcp.Tool{Name: "tv_x", Meta: mcp.Meta{pluginkit.MetaTimeout: 0.0}},
			audience: hub.AudienceAssistant, timeout: 30 * time.Second, warnings: 1},
		{name: "string timeout", tool: &mcp.Tool{Name: "tv_x", Meta: mcp.Meta{pluginkit.MetaTimeout: "10"}},
			audience: hub.AudienceAssistant, timeout: 30 * time.Second, warnings: 1},
		{name: "inventory", tool: pluginkit.Inventory(&mcp.Tool{Name: "tv_list", Annotations: ro}),
			audience: hub.AudienceAssistant, timeout: 30 * time.Second, inventory: true},
		{name: "inventory not read-only", tool: pluginkit.Inventory(&mcp.Tool{Name: "tv_list"}),
			audience: hub.AudienceAssistant, timeout: 30 * time.Second, warnings: 1},
		{name: "unknown role", tool: &mcp.Tool{Name: "tv_list", Annotations: ro, Meta: mcp.Meta{pluginkit.MetaRole: "x"}},
			audience: hub.AudienceAssistant, timeout: 30 * time.Second, warnings: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warnings, err := convertTool("tv", tt.tool)
			if tt.skip {
				if err == nil {
					t.Fatal("not skipped")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Audience != tt.audience || got.Timeout != tt.timeout || got.Inventory != tt.inventory || len(warnings) != tt.warnings {
				t.Errorf("got audience %q timeout %v inventory %v warnings %q", got.Audience, got.Timeout, got.Inventory, warnings)
			}
			if string(got.InputSchema) != `{"type":"object"}` || got.PluginID != "tv" {
				t.Errorf("schema %s plugin %q", got.InputSchema, got.PluginID)
			}
		})
	}
}
