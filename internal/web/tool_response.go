package web

import (
	"encoding/json"
	"fmt"
	"m365-native/internal/chathub"
	"net/http"
	"strings"
	"time"
)

// toolPlanSummary tells the client what will happen before the structured call.
// It must describe the concrete operation, rather than repeat a generic phrase.
func toolPlanSummary(calls []detectedToolCall) string {
	if len(calls) == 0 {
		return "I will organize the current request and continue processing."
	}
	plans := make([]string, 0, len(calls))
	for _, c := range calls {
		plans = append(plans, toolPlan(c))
	}
	return strings.Join(plans, "\n\n")
}

func toolPlan(c detectedToolCall) string {
	return toolPlanFor(c.Name, c.Arguments)
}

func toolPlanFor(name string, arguments []byte) string {
	var args map[string]any
	_ = json.Unmarshal(arguments, &args)
	verb := "call " + name
	purpose := "retrieve information from this tool"
	var target string
	for _, key := range []string{"command", "cmd", "path", "query", "url", "input", "prompt"} {
		if s, ok := args[key].(string); ok && strings.TrimSpace(s) != "" {
			target = strings.TrimSpace(s)
			break
		}
	}
	switch {
	case strings.Contains(name, "shell") || strings.Contains(name, "exec") || strings.Contains(name, "command"):
		verb = "execute workspace command"
		purpose = "read project state, run checks, or execute user-specified commands"
	case strings.Contains(name, "read") || strings.Contains(name, "file"):
		verb = "read file content"
		purpose = "inspect file content and continue based on findings"
	case strings.Contains(name, "write") || strings.Contains(name, "edit") || strings.Contains(name, "update"):
		verb = "modify project files"
		purpose = "apply requested changes while preserving existing logic"
	case strings.Contains(name, "search") || strings.Contains(name, "browser") || strings.Contains(name, "fetch"):
		verb = "query external information"
		purpose = "retrieve relevant information for the current response"
	}
	if target != "" {
		if len([]rune(target)) > 180 {
			target = string([]rune(target)[:180]) + "…"
		}
		return fmt.Sprintf("I will: %s.\n\nPurpose: %s.\n\nNext: continue after the result is returned.", verb+": "+target, purpose)
	}
	return fmt.Sprintf("I will: %s.\n\nPurpose: %s.\n\nNext: continue after the result is returned.", verb, purpose)
}

func toolPlanSummaryFromMaps(calls []any) string {
	converted := make([]detectedToolCall, 0, len(calls))
	for _, raw := range calls {
		tc, _ := raw.(map[string]any)
		fn, _ := tc["function"].(map[string]any)
		name, _ := fn["name"].(string)
		args, _ := fn["arguments"].(string)
		converted = append(converted, detectedToolCall{Name: name, Arguments: []byte(args)})
	}
	return toolPlanSummary(converted)
}

func writeToolResponse(w http.ResponseWriter, id, model string, stream bool, calls []detectedToolCall, res chathub.Result, preambleSent ...bool) error {
	toolCalls := toolCallMaps(calls)
	summary := toolPlanSummary(calls)
	msg := map[string]any{"role": "assistant", "content": summary, "reasoning_content": summary, "tool_calls": toolCalls}
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, _ := w.(http.Flusher)
		emit := func(v any) {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(v))
			if flusher != nil {
				flusher.Flush()
			}
		}
		base := func(delta map[string]any, finish any) map[string]any {
			return map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		}
		if len(preambleSent) == 0 || !preambleSent[0] {
			emit(base(map[string]any{"role": "assistant", "content": summary, "reasoning_content": summary}, nil))
		}
		for i, tc := range calls {
			typ := tc.Type
			if typ == "" {
				typ = "function"
			}
			emit(base(map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": tc.ID, "type": typ, "function": map[string]any{"name": tc.Name, "arguments": string(tc.Arguments)}}}}, nil))
		}
		emit(base(map[string]any{}, "tool_calls"))
		fmt.Fprint(w, "data: [DONE]\n\n")
		return nil
	}
	jsonOut(w, map[string]any{"id": id, "object": "chat.completion", "model": model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "tool_calls"}}, "m365": compatM365Metadata(res)})
	return nil
}
