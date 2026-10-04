package gatekeeper

import "encoding/json"

// JSON-RPC 2.0 error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// notification reports whether the message has no id and so expects no answer.
func (r request) notification() bool { return len(r.ID) == 0 }

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func result(id json.RawMessage, v any) response {
	return response{JSONRPC: "2.0", ID: id, Result: v}
}

func failure(id json.RawMessage, code int, msg string) response {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
}

// toolResult is the result of tools/call: text for the model, and whether the call failed.
type toolResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textResult(s string) toolResult { return toolResult{Content: []content{{Type: "text", Text: s}}} }
func errorResult(s string) toolResult {
	return toolResult{Content: []content{{Type: "text", Text: s}}, IsError: true}
}
