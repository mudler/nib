# Preserve empty tool outputs in Responses requests

## Goal

nib must preserve a successful empty tool result when it builds a Responses API
request. The request must contain `"output":""` instead of omitting the required
`output` field.

This behavior applies to the Codex and OpenAI Responses adapters.

## Cause

Both adapters represent each request input as a Go struct. The `Output` field
uses the `json:"output,omitempty"` tag. Go therefore omits `output` when a tool
returns an empty string.

The Responses API requires `output` on each `function_call_output` item. Codex
rejects the malformed history with a `missing_required_parameter` error.

## Design

Each adapter must serialize request input items according to the item type:

- A `function_call_output` item must always contain `output`.
- An empty successful result must remain an empty string.
- A nonempty result must remain unchanged.
- Message and function-call items must omit `output` when it does not apply.

The implementation must keep `Output` as a string. A custom JSON marshaler must
control field presence without adding `output` to unrelated item types.

Codex repair must retain its current behavior for interrupted calls. A missing
result is not an empty successful result. Repair must continue to use the
existing explanatory placeholder when it synthesizes an interrupted result.

## Data flow

1. Cogito records a tool response as a tool-role chat message.
2. The adapter translates the message to a `function_call_output` input item.
3. The custom marshaler includes the `output` key for that item type.
4. The JSON encoder writes an empty string when the tool response is empty.
5. The Responses API receives a valid call and output pair.

## Error handling

This change does not add retries or replace values. It prevents the invalid
request before the adapter sends it.

The adapters must continue to report other API validation errors without
changing them.

## Tests

Add regression tests for both adapters. The tests must check these cases:

- An empty tool result serializes as `"output":""`.
- A nonempty tool result keeps its value.
- A user or assistant message does not contain `output`.
- A function-call item does not contain `output`.
- Codex repair keeps the interrupted-result placeholder.

Run the focused adapter tests first. Then run `go test ./...`.

## Documentation

No README change is required. This change restores the required wire format and
does not change a user-facing interface.

## Out of scope

- Replacing empty successful results with explanatory text.
- Changing the internal tool-result type to a pointer.
- Changing retry behavior for rejected requests.
- Changing Codex repair for interrupted tool calls.
