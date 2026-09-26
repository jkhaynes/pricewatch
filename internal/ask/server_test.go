package ask

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// wantTools is every tool the server offers. Each task that adds a tool adds it here.
var wantTools = []string{"collection_value", "top_cards", "find_cards", "price_history", "movers", "pipeline_status", "query"}

func TestServerListsEveryTool(t *testing.T) {
	cs := connect(t, collection(t), "")
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
	}
	slices.Sort(got)
	want := slices.Sorted(slices.Values(wantTools))
	if !slices.Equal(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
}

func TestAnswersSayHowFreshTheyAre(t *testing.T) {
	cs := connect(t, collection(t), "refresh failed")
	got := call[ValueOut](t, cs, "collection_value", map[string]any{})
	if got.DataAsOf == nil || time.Since(*got.DataAsOf) > time.Minute {
		t.Errorf("data_as_of = %v, want the last run's finish", got.DataAsOf)
	}
	if got.Stale != "refresh failed" || got.Result.Total != 7.18 {
		t.Errorf("answer = %+v", got)
	}
}

func TestNoDatabaseIsAToolError(t *testing.T) {
	acq := func(context.Context) (Store, func(), string, error) {
		return nil, nil, "", errors.New("no local copy, and the download failed")
	}
	ct, sst := mcp.NewInMemoryTransports()
	ctx := t.Context()
	if _, err := NewServer(acq, "pw", time.Now).Connect(ctx, sst, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "collection_value", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("a missing database must be a tool error, not a protocol error: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !res.IsError || !strings.Contains(text, "download failed") {
		t.Errorf("result = %+v, want an error explaining why", res)
	}
}
