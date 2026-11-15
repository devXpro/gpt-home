package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

// The owner renames the home in the admin: the instructions, the MCP title and the version fingerprint follow at
// once, without a tool change.
func TestSetHomeName(t *testing.T) {
	e := newEnv(t, Options{HomeName: "Мой шалаш"})
	serverInfo := func() (title, version string) {
		cs, err := e.connect(t, e.roman)
		if err != nil {
			t.Fatal(err)
		}
		defer cs.Close()
		si := cs.InitializeResult().ServerInfo
		return si.Title, si.Version
	}
	instructions := func() string {
		_, _, body := e.do(t, "GET", "/api/v1/tools", e.roman, "")
		var m struct {
			Instructions string `json:"instructions"`
		}
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatal(err)
		}
		return m.Instructions
	}
	title1, v1 := serverInfo()
	if title1 != "Мой шалаш" || !strings.Contains(instructions(), `"Мой шалаш"`) {
		t.Fatalf("before: title %q, instructions %q", title1, instructions())
	}

	e.gw.SetHomeName("Дача")
	title2, v2 := serverInfo()
	if title2 != "Дача" || !strings.Contains(instructions(), `"Дача"`) || strings.Contains(instructions(), "Мой шалаш") {
		t.Fatalf("after: title %q, instructions %q", title2, instructions())
	}
	if v2 == v1 {
		t.Fatal("the version fingerprint did not move with the home name")
	}

	// The same name again keeps the snapshot.
	snap := e.gw.current()
	e.gw.SetHomeName("Дача")
	if e.gw.current() != snap {
		t.Fatal("an unchanged name rebuilt the snapshot")
	}
}
