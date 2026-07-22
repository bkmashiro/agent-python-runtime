package capability

import "context"

type Status string

const (
	StatusOK      Status = "ok"
	StatusDenied  Status = "denied"
	StatusError   Status = "error"
	StatusTimeout Status = "timeout"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ToolResponse struct {
	CallID string          `json:"call_id"`
	Status Status          `json:"status"`
	Result FetchManyResult `json:"result"`
	Error  *Error          `json:"error"`
}

type FetchManyResult struct {
	Items []FetchItem `json:"items"`
}

type FetchItem struct {
	RequestID   string `json:"request_id"`
	Status      Status `json:"status"`
	HTTPStatus  int    `json:"http_status,omitempty"`
	Body        string `json:"body,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Error       *Error `json:"error"`
}

type ResolvedRequest struct {
	URL     string
	Headers map[string]string
}

type FetchOutput struct {
	StatusCode  int
	Body        []byte
	ContentType string
}

type Fetcher interface {
	Fetch(ctx context.Context, request ResolvedRequest, maxResponseBytes uint32) (FetchOutput, error)
}
