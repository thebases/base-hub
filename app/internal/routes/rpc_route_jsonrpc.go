package routes

// File overview:
// - Responsibility: JSON-RPC request/response envelope helpers shared across the rpc route handlers.
// - Key symbols: `decodeJSONRPCRequest`, `newJSONRPCErrorResponse`, `newJSONRPCSuccessResponse`.

import "encoding/json"

// decodeJSONRPCRequest decodes the jsonrpc request handled by this package.
func decodeJSONRPCRequest(body []byte) (jsonRPCRequest, error) {
	var request jsonRPCRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return jsonRPCRequest{}, err
	}
	if request.Params == nil {
		request.Params = map[string]any{}
	}
	return request, nil
}

// newJSONRPCErrorResponse constructs and returns the jsonrpc error response handled by this package.
func newJSONRPCErrorResponse(id any, code int, err error) jsonRPCResponse {
	message := "unknown error"
	if err != nil {
		message = err.Error()
	}
	return jsonRPCResponse{
		ID: id,
		Error: &JSONRPCErrorDoc{
			Code:    code,
			Message: message,
		},
	}
}

// newJSONRPCSuccessResponse constructs and returns the jsonrpc success response handled by this package.
func newJSONRPCSuccessResponse(id, result any) jsonRPCResponse {
	return jsonRPCResponse{
		ID:     id,
		Result: result,
	}
}
