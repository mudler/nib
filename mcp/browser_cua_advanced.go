package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/xlog"
)

const (
	browserPointerDescription = "Perform a hover, right-click, double-click, scroll, or drag in the exact Cua browser tab and return a fresh semantic snapshot."
	browserDialogDescription  = "Inspect or resolve the current page-owned JavaScript dialog in the exact Cua browser tab."
)

type BrowserPointerInput struct {
	Action         string   `json:"action"`
	InputRoute     string   `json:"input_route,omitempty"`
	Ref            string   `json:"ref,omitempty"`
	X              *float64 `json:"x,omitempty"`
	Y              *float64 `json:"y,omitempty"`
	DestinationRef string   `json:"destination_ref,omitempty"`
	ToX            *float64 `json:"to_x,omitempty"`
	ToY            *float64 `json:"to_y,omitempty"`
	DeltaX         *float64 `json:"delta_x,omitempty"`
	DeltaY         *float64 `json:"delta_y,omitempty"`
}

type BrowserDialogInput struct {
	Action       string  `json:"action"`
	DialogID     string  `json:"dialog_id,omitempty"`
	PromptText   *string `json:"prompt_text,omitempty"`
	DeliveryMode string  `json:"delivery_mode,omitempty"`
}

type BrowserDialogOutput struct {
	BrowserOutcome
	Present      bool   `json:"present,omitempty"`
	DialogID     string `json:"dialog_id,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Action       string `json:"action,omitempty"`
	Snapshot     string `json:"snapshot,omitempty"`
	ElementCount int    `json:"element_count,omitempty"`
}

type validatedPointerInput struct {
	action         string
	inputRoute     string
	ref            string
	x              *float64
	y              *float64
	destinationRef string
	toX            *float64
	toY            *float64
	deltaX         *float64
	deltaY         *float64
}

type cuaDialogResult struct {
	Status   string `json:"status"`
	TargetID string `json:"target_id"`
	TabID    string `json:"tab_id"`
	Present  bool   `json:"present"`
	DialogID string `json:"dialog_id"`
	Kind     string `json:"kind"`
	Action   string `json:"action"`
}

type cuaPointerResult struct {
	Status   string `json:"status"`
	TargetID string `json:"target_id"`
	TabID    string `json:"tab_id"`
	Action   string `json:"action"`
	Route    string `json:"route"`
}

func (b *cuaBrowserServer) browserPointer(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserPointerInput,
) (*mcp.CallToolResult, BrowserOutput, error) {
	validated, err := validateBrowserPointerInput(in)
	if err != nil {
		return nil, BrowserOutput{}, err
	}

	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserOutput{}, err
	}

	args := map[string]any{
		"action": validated.action, "input_route": validated.inputRoute,
	}
	if validated.ref != "" {
		element, refErr := b.pointerRef(validated.ref, validated.action)
		if refErr != nil {
			return nil, BrowserOutput{}, refErr
		}
		args["ref"] = element.Raw
	} else {
		args["x"] = *validated.x
		args["y"] = *validated.y
	}
	if validated.destinationRef != "" {
		element, refErr := b.actionableRef(validated.destinationRef, "pointer")
		if refErr != nil {
			return nil, BrowserOutput{}, refErr
		}
		args["destination_ref"] = element.Raw
	} else if validated.toX != nil {
		args["to_x"] = *validated.toX
		args["to_y"] = *validated.toY
	}
	if validated.deltaX != nil {
		args["delta_x"] = *validated.deltaX
	}
	if validated.deltaY != nil {
		args["delta_y"] = *validated.deltaY
	}

	exact, err := b.exactArgs(args)
	if err != nil {
		return nil, BrowserOutput{}, err
	}
	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	return b.mutatePointerThenSnapshot(actionCtx, exact, validated.action, validated.inputRoute)
}

func (b *cuaBrowserServer) mutatePointerThenSnapshot(
	ctx context.Context,
	args map[string]any,
	action string,
	route string,
) (*mcp.CallToolResult, BrowserOutput, error) {
	var mutation cuaPointerResult
	_, refusal, err := b.callResult(ctx, "browser_pointer", args, &mutation)
	if err != nil {
		if errors.Is(err, errCUAInvalidStatus) {
			b.clearTabScopedCapabilities()
		}
		return nil, BrowserOutput{}, err
	}
	if refusal != nil {
		b.invalidateOnRefusal(refusal)
		return b.browserRefusalResult(refusal, args)
	}

	// Cua accepted the mutation. Every capability from the pre-action page is
	// stale even when the success envelope itself is malformed or mismatched.
	b.clearTabScopedCapabilities()
	if err := b.validatePointerResult(mutation, action, route); err != nil {
		return nil, BrowserOutput{}, err
	}
	result, output, _, err := b.snapshotInternal(ctx, false, false, false)
	if err != nil {
		return nil, BrowserOutput{}, fmt.Errorf("browser pointer %s succeeded but post-action snapshot failed: %w", action, err)
	}
	if output.Status == "ok" {
		result = textResult(output.Snapshot)
	}
	return result, output, nil
}

func (b *cuaBrowserServer) validatePointerResult(result cuaPointerResult, action, route string) error {
	b.mu.Lock()
	targetID, tabID := b.targetID, b.selectedTab
	b.mu.Unlock()
	if result.TargetID == "" || result.TabID == "" || result.Action == "" || result.Route == "" {
		return errors.New("Cua browser_pointer success omitted exact target, tab, action, or route")
	}
	if result.TargetID != targetID {
		b.observeTargetGeneration(result.TargetID)
		return errors.New("Cua browser_pointer returned a different browser target")
	}
	if result.TabID != tabID {
		return errors.New("Cua browser_pointer returned a different browser tab")
	}
	if result.Action != action || result.Route != route {
		return errors.New("Cua browser_pointer returned a different action or input route")
	}
	return nil
}

func validateBrowserPointerInput(in BrowserPointerInput) (validatedPointerInput, error) {
	action := strings.TrimSpace(in.Action)
	if !oneOf(action, "hover", "right_click", "double_click", "scroll", "drag") {
		return validatedPointerInput{}, errors.New("browser_pointer action must be hover, right_click, double_click, scroll, or drag")
	}
	route := strings.TrimSpace(in.InputRoute)
	if route == "" {
		route = "trusted"
	}
	if !oneOf(route, "trusted", "dom_event") {
		return validatedPointerInput{}, errors.New("browser_pointer input_route must be trusted or dom_event")
	}

	ref := strings.TrimSpace(in.Ref)
	hasCoordinates, err := finiteCoordinatePair(in.X, in.Y, "x", "y")
	if err != nil {
		return validatedPointerInput{}, err
	}
	if (ref != "") == hasCoordinates {
		return validatedPointerInput{}, errors.New("browser_pointer needs exactly one origin: ref or paired x/y coordinates")
	}
	if route == "dom_event" && ref == "" {
		return validatedPointerInput{}, errors.New("browser_pointer input_route=dom_event requires a ref origin")
	}

	destinationRef := strings.TrimSpace(in.DestinationRef)
	hasDestinationCoordinates, err := finiteCoordinatePair(in.ToX, in.ToY, "to_x", "to_y")
	if err != nil {
		return validatedPointerInput{}, err
	}
	if destinationRef != "" && hasDestinationCoordinates {
		return validatedPointerInput{}, errors.New("browser_pointer accepts either destination_ref or paired to_x/to_y coordinates, not both")
	}
	hasDestination := destinationRef != "" || hasDestinationCoordinates
	if action == "drag" && !hasDestination {
		return validatedPointerInput{}, errors.New("browser_pointer action=drag requires destination_ref or paired to_x/to_y coordinates")
	}
	if action != "drag" && hasDestination {
		return validatedPointerInput{}, errors.New("browser_pointer destination fields are valid only for action=drag")
	}
	if action == "drag" && ref == "" && destinationRef != "" {
		return validatedPointerInput{}, errors.New("browser_pointer cannot prove a coordinate origin shares a frame with destination_ref; use two refs or two coordinate pairs")
	}

	if err := finiteOptional(in.DeltaX, "delta_x"); err != nil {
		return validatedPointerInput{}, err
	}
	if err := finiteOptional(in.DeltaY, "delta_y"); err != nil {
		return validatedPointerInput{}, err
	}
	if action == "scroll" {
		deltaX, deltaY := float64(0), float64(0)
		if in.DeltaX != nil {
			deltaX = *in.DeltaX
		}
		if in.DeltaY != nil {
			deltaY = *in.DeltaY
		}
		if deltaX == 0 && deltaY == 0 {
			return validatedPointerInput{}, errors.New("browser_pointer action=scroll requires a non-zero delta_x or delta_y")
		}
	} else if in.DeltaX != nil || in.DeltaY != nil {
		return validatedPointerInput{}, errors.New("browser_pointer delta fields are valid only for action=scroll")
	}

	return validatedPointerInput{
		action: action, inputRoute: route, ref: ref, x: in.X, y: in.Y,
		destinationRef: destinationRef, toX: in.ToX, toY: in.ToY,
		deltaX: in.DeltaX, deltaY: in.DeltaY,
	}, nil
}

func finiteCoordinatePair(x, y *float64, xName, yName string) (bool, error) {
	if (x == nil) != (y == nil) {
		return false, fmt.Errorf("browser_pointer requires both %s and %s, or neither", xName, yName)
	}
	if x == nil {
		return false, nil
	}
	if !finite(*x) || !finite(*y) {
		return false, fmt.Errorf("browser_pointer %s and %s must be finite numbers", xName, yName)
	}
	return true, nil
}

func finiteOptional(value *float64, name string) error {
	if value != nil && !finite(*value) {
		return fmt.Errorf("browser_pointer %s must be a finite number", name)
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func (b *cuaBrowserServer) pointerRef(alias, action string) (cuaElement, error) {
	if action != "scroll" {
		return b.actionableRef(alias, "pointer")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	element, ok := b.refs[alias]
	if !ok || element.Raw == "" {
		return cuaElement{}, fmt.Errorf("unknown or stale element ref %q; call browser_snapshot", alias)
	}
	if !element.Actions["scroll"] && !element.Actions["pointer"] {
		return cuaElement{}, fmt.Errorf("element ref %q does not support scroll or pointer; call browser_snapshot", alias)
	}
	return element, nil
}

func (b *cuaBrowserServer) browserDialog(
	ctx context.Context,
	_ *mcp.CallToolRequest,
	in BrowserDialogInput,
) (*mcp.CallToolResult, BrowserDialogOutput, error) {
	action := strings.TrimSpace(in.Action)
	if !oneOf(action, "inspect", "accept", "dismiss") {
		return nil, BrowserDialogOutput{}, errors.New("browser_dialog action must be inspect, accept, or dismiss")
	}
	delivery := strings.TrimSpace(in.DeliveryMode)
	if delivery == "" {
		delivery = "background"
	}
	if !oneOf(delivery, "background", "foreground") {
		return nil, BrowserDialogOutput{}, errors.New("browser_dialog delivery_mode must be background or foreground")
	}
	if action == "inspect" && in.DialogID != "" {
		return nil, BrowserDialogOutput{}, errors.New("browser_dialog dialog_id is valid only for accept or dismiss")
	}
	if action == "inspect" && in.PromptText != nil {
		return nil, BrowserDialogOutput{}, errors.New("browser_dialog prompt_text is valid only when accepting a prompt")
	}

	b.actionMu.Lock()
	defer b.actionMu.Unlock()
	if err := b.requirePrepared(); err != nil {
		return nil, BrowserDialogOutput{}, err
	}
	actionCtx, cancel := context.WithTimeout(ctx, browserActionTimeout)
	defer cancel()
	if action == "inspect" {
		return b.inspectDialog(actionCtx, delivery)
	}
	return b.resolveDialog(actionCtx, in, action, delivery)
}

func (b *cuaBrowserServer) inspectDialog(
	ctx context.Context,
	delivery string,
) (*mcp.CallToolResult, BrowserDialogOutput, error) {
	args, err := b.exactArgs(map[string]any{"action": "inspect", "delivery_mode": delivery})
	if err != nil {
		return nil, BrowserDialogOutput{}, err
	}
	result, refusal, usedArgs, err := b.callDialogInspect(ctx, args)
	if err != nil {
		if errors.Is(err, errCUAInvalidStatus) {
			b.clearDialogCapability()
		}
		return nil, BrowserDialogOutput{}, err
	}
	if refusal != nil {
		return b.dialogRefusalResult(refusal, usedArgs)
	}

	// A successful inspection supersedes every previously cached dialog
	// generation even when the returned capability is malformed.
	b.clearDialogCapability()
	if err := b.validateDialogBinding(result.TargetID, result.TabID); err != nil {
		return nil, BrowserDialogOutput{}, err
	}
	if !result.Present {
		if result.DialogID != "" || result.Kind != "" {
			return nil, BrowserDialogOutput{}, errors.New("Cua browser_dialog returned an absent dialog with capability fields")
		}
		output := BrowserDialogOutput{BrowserOutcome: BrowserOutcome{Status: "ok"}}
		return textResult("no page-owned JavaScript dialog is open"), output, nil
	}
	if result.DialogID == "" || !validDialogKind(result.Kind) {
		return nil, BrowserDialogOutput{}, errors.New("Cua browser_dialog inspect returned an invalid dialog capability")
	}
	b.mu.Lock()
	b.dialogID = result.DialogID
	b.dialogKind = result.Kind
	b.mu.Unlock()
	output := BrowserDialogOutput{
		BrowserOutcome: BrowserOutcome{Status: "ok"},
		Present:        true,
		DialogID:       result.DialogID,
		Kind:           result.Kind,
	}
	return textResult(result.Kind + " JavaScript dialog is open"), output, nil
}

func (b *cuaBrowserServer) callDialogInspect(
	ctx context.Context,
	args map[string]any,
) (cuaDialogResult, *cuaRefusal, map[string]any, error) {
	var result cuaDialogResult
	_, refusal, err := b.callResult(ctx, "browser_dialog", args, &result)
	if err != nil || refusal == nil || !isRebindableReadRefusal(refusal.Code) {
		if refusal != nil {
			b.invalidateOnRefusal(refusal)
		}
		return result, refusal, args, err
	}

	b.mu.Lock()
	oldTarget, oldSelected := b.targetID, b.selectedTab
	b.mu.Unlock()
	b.invalidateOnRefusal(refusal)
	if rebindRefusal, rebindErr := b.rebindExact(ctx, oldTarget, oldSelected); rebindErr != nil {
		return cuaDialogResult{}, nil, args, rebindErr
	} else if rebindRefusal != nil {
		return cuaDialogResult{}, rebindRefusal, args, nil
	}
	retryArgs, err := b.exactArgs(map[string]any{
		"action": "inspect", "delivery_mode": args["delivery_mode"],
	})
	if err != nil {
		return cuaDialogResult{}, nil, args, err
	}
	result = cuaDialogResult{}
	_, refusal, err = b.callResult(ctx, "browser_dialog", retryArgs, &result)
	if refusal != nil {
		b.invalidateOnRefusal(refusal)
	}
	return result, refusal, retryArgs, err
}

func (b *cuaBrowserServer) resolveDialog(
	ctx context.Context,
	in BrowserDialogInput,
	action string,
	delivery string,
) (*mcp.CallToolResult, BrowserDialogOutput, error) {
	b.mu.Lock()
	dialogID, kind := b.dialogID, b.dialogKind
	b.mu.Unlock()
	if dialogID == "" || in.DialogID == "" || in.DialogID != dialogID || !validDialogKind(kind) {
		return nil, BrowserDialogOutput{}, errors.New("browser_dialog requires the current dialog_id from browser_dialog action=inspect")
	}
	if in.PromptText != nil && (action != "accept" || kind != "prompt") {
		return nil, BrowserDialogOutput{}, errors.New("browser_dialog prompt_text is valid only when accepting a prompt")
	}

	extra := map[string]any{
		"action": action, "dialog_id": dialogID, "delivery_mode": delivery,
	}
	if in.PromptText != nil {
		extra["prompt_text"] = *in.PromptText
	}
	args, err := b.exactArgs(extra)
	if err != nil {
		return nil, BrowserDialogOutput{}, err
	}
	var resolution cuaDialogResult
	_, refusal, err := b.callResult(ctx, "browser_dialog", args, &resolution)
	if err != nil {
		if errors.Is(err, errCUAInvalidStatus) {
			b.clearTabScopedCapabilities()
		}
		return nil, BrowserDialogOutput{}, b.publicCallError(err, args)
	}
	if refusal != nil {
		b.invalidateOnRefusal(refusal)
		return b.dialogRefusalResult(refusal, args)
	}

	// Resolution has happened and must never be repeated, even if response
	// validation or the verification snapshot fails.
	b.clearTabScopedCapabilities()
	if err := b.validateDialogBinding(resolution.TargetID, resolution.TabID); err != nil {
		return nil, BrowserDialogOutput{}, err
	}
	if resolution.DialogID != dialogID || resolution.Kind != kind || resolution.Action != action {
		return nil, BrowserDialogOutput{}, errors.New("Cua browser_dialog resolution returned a different dialog capability")
	}

	snapshotResult, snapshotOutput, _, err := b.snapshotInternal(ctx, false, false, false)
	if err != nil {
		return nil, BrowserDialogOutput{}, fmt.Errorf("%s dialog succeeded but post-action snapshot failed: %w", action, b.publicCallError(err, args))
	}
	if snapshotOutput.Refusal != nil {
		b.mu.Lock()
		snapshotOutput.Refusal = b.aliasStateLocked().resanitizePublicRefusal(snapshotOutput.Refusal, args)
		b.mu.Unlock()
		snapshotResult = textResult(snapshotOutput.Refusal.Message)
	}
	output := BrowserDialogOutput{
		BrowserOutcome: snapshotOutput.BrowserOutcome,
		DialogID:       dialogID,
		Kind:           kind,
		Action:         action,
		Snapshot:       snapshotOutput.Snapshot,
		ElementCount:   snapshotOutput.ElementCount,
	}
	if output.Status == "ok" {
		snapshotResult = textResult(output.Snapshot)
	}
	return snapshotResult, output, nil
}

func (b *cuaBrowserServer) publicCallError(err error, args map[string]any) error {
	if err == nil {
		return nil
	}
	b.mu.Lock()
	public := b.aliasStateLocked().publicError(err, args)
	b.mu.Unlock()
	return public
}

func validDialogKind(kind string) bool {
	return oneOf(kind, "alert", "confirm", "prompt", "beforeunload")
}

func (b *cuaBrowserServer) clearDialogCapability() {
	b.mu.Lock()
	b.dialogID = ""
	b.dialogKind = ""
	b.mu.Unlock()
}

func (b *cuaBrowserServer) validateDialogBinding(targetID, tabID string) error {
	b.mu.Lock()
	currentTarget, currentTab := b.targetID, b.selectedTab
	b.mu.Unlock()
	if targetID == "" || tabID == "" {
		return errors.New("Cua browser_dialog result omitted exact target or tab identity")
	}
	if targetID != currentTarget {
		b.observeTargetGeneration(targetID)
		return errors.New("Cua browser_dialog returned a different browser target")
	}
	if tabID != currentTab {
		b.clearTabScopedCapabilities()
		return errors.New("Cua browser_dialog returned a different browser tab")
	}
	return nil
}

func (b *cuaBrowserServer) dialogRefusalResult(
	refusal *cuaRefusal,
	args map[string]any,
) (*mcp.CallToolResult, BrowserDialogOutput, error) {
	b.mu.Lock()
	public := b.aliasStateLocked().publicRefusal(refusal, args)
	b.mu.Unlock()
	output := BrowserDialogOutput{BrowserOutcome: BrowserOutcome{Status: "refused", Refusal: public}}
	return textResult(public.Message), output, nil
}

func registerCUABrowserAdvancedTools(server *mcp.Server, browser *cuaBrowserServer) {
	pointerTool := &mcp.Tool{Name: "browser_pointer", Description: browserPointerDescription}
	if schema, err := browserPointerInputSchema(); err != nil {
		xlog.Warn("browser: could not build pointer enum schema, falling back to inferred", "err", err)
	} else {
		pointerTool.InputSchema = schema
	}
	mcp.AddTool(server, pointerTool, browser.browserPointer)

	dialogTool := &mcp.Tool{Name: "browser_dialog", Description: browserDialogDescription}
	if schema, err := browserDialogInputSchema(); err != nil {
		xlog.Warn("browser: could not build dialog enum schema, falling back to inferred", "err", err)
	} else {
		dialogTool.InputSchema = schema
	}
	mcp.AddTool(server, dialogTool, browser.browserDialog)
}

func browserPointerInputSchema() (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[BrowserPointerInput](nil)
	if err != nil {
		return nil, err
	}
	setSchemaEnum(schema, "action", "hover", "right_click", "double_click", "scroll", "drag")
	setSchemaEnum(schema, "input_route", "trusted", "dom_event")
	return schema, nil
}

func browserDialogInputSchema() (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[BrowserDialogInput](nil)
	if err != nil {
		return nil, err
	}
	setSchemaEnum(schema, "action", "inspect", "accept", "dismiss")
	setSchemaEnum(schema, "delivery_mode", "background", "foreground")
	return schema, nil
}

func setSchemaEnum(schema *jsonschema.Schema, property string, values ...string) {
	if schema == nil || schema.Properties[property] == nil {
		return
	}
	enum := make([]any, len(values))
	for index, value := range values {
		enum[index] = value
	}
	schema.Properties[property].Enum = enum
}
