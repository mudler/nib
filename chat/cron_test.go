package chat

import (
	"strings"
	"testing"
)

func TestCronToolsCallCallbacks(t *testing.T) {
	var created CronRequest
	create := func(r CronRequest) string { created = r; return "created loop-1" }
	list := func() string { return "1 loop active" }
	var deleted string
	del := func(id string) string { deleted = id; return "deleted " + id }

	cdef := cronToolDefinition(create)
	ldef := cronListToolDefinition(list)
	ddef := cronDeleteToolDefinition(del)
	if cdef == nil || ldef == nil || ddef == nil {
		t.Fatal("nil tool definition")
	}
	// Schema generation must not panic (no map fields in arg structs).
	if cdef.Tool().Function == nil || cdef.Tool().Function.Name != "cron" {
		t.Fatalf("cron def: %+v", cdef.Tool())
	}
	if ldef.Tool().Function == nil || ldef.Tool().Function.Name != "cron_list" {
		t.Fatalf("cron_list def: %+v", ldef.Tool())
	}
	if ddef.Tool().Function == nil || ddef.Tool().Function.Name != "cron_delete" {
		t.Fatalf("cron_delete def: %+v", ddef.Tool())
	}

	ct := &cronTool{create: create}
	out, _, _ := ct.Run(map[string]any{"expr": "*/5 * * * *", "prompt": "/foo", "recurring": true, "durable": false})
	if !strings.Contains(out, "loop-1") {
		t.Fatalf("create out: %q", out)
	}
	if created.Expr != "*/5 * * * *" || created.Prompt != "/foo" || !created.Recurring {
		t.Fatalf("create req: %+v", created)
	}

	dt := &cronDeleteTool{del: del}
	dt.Run(map[string]any{"id": "loop-1"})
	if deleted != "loop-1" {
		t.Fatalf("delete id: %q", deleted)
	}

	lt := &cronListTool{list: list}
	if out, _, _ := lt.Run(nil); !strings.Contains(out, "loop") {
		t.Fatalf("list out: %q", out)
	}
}

func TestCronRecurringDefault(t *testing.T) {
	var got CronRequest
	ct := &cronTool{create: func(r CronRequest) string { got = r; return "" }}
	ct.Run(map[string]any{"expr": "*/5 * * * *", "prompt": "/foo"}) // recurring omitted
	if !got.Recurring {
		t.Fatalf("omitted recurring should default true, got %+v", got)
	}
	ct.Run(map[string]any{"expr": "*/5 * * * *", "prompt": "/foo", "recurring": false})
	if got.Recurring {
		t.Fatalf("explicit recurring=false should be false, got %+v", got)
	}
}
