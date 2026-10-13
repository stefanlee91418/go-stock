package agent

import (
	"bytes"
	"encoding/json"
	"go-stock/backend/logger"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const modelLogRespPeekLimit = 4096

// modelCallLogTransport 记录每次模型 HTTP 调用摘要到 logs/model.log。
// 不消费完整响应体（流式 SSE 需原样透传）；Close 时根据窥探窗口提取 finish_reason / usage。
type modelCallLogTransport struct {
	base http.RoundTripper
}

func (t *modelCallLogTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	reqSummary := readAndSummarizeModelRequest(req)

	resp, err := t.base.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil {
		logger.ModelErrorf("model HTTP fail method=%s url=%s cost=%s req={%s} err=%v",
			req.Method, redactURL(req.URL.String()), elapsed.Round(time.Millisecond), reqSummary, err)
		return resp, err
	}

	status := resp.StatusCode
	ct := resp.Header.Get("Content-Type")
	stream := strings.Contains(strings.ToLower(ct), "text/event-stream") ||
		strings.Contains(strings.ToLower(ct), "stream")

	if resp.Body != nil {
		resp.Body = &modelRespBodyProbe{
			rc:     resp.Body,
			stream: stream,
			onClose: func(peek []byte) {
				meta := summarizeModelResponseMeta(peek)
				logFn := logger.ModelInfof
				if status >= 400 {
					logFn = logger.ModelErrorf
				}
				logFn("model HTTP done method=%s url=%s status=%d cost=%s stream=%v req={%s} resp={%s}",
					req.Method, redactURL(req.URL.String()), status, elapsed.Round(time.Millisecond),
					stream, reqSummary, meta)
			},
		}
	} else {
		logger.ModelInfof("model HTTP done method=%s url=%s status=%d cost=%s stream=%v req={%s} resp={empty body}",
			req.Method, redactURL(req.URL.String()), status, elapsed.Round(time.Millisecond),
			stream, reqSummary)
	}
	return resp, nil
}

type modelRespBodyProbe struct {
	rc      io.ReadCloser
	stream  bool
	buf     bytes.Buffer
	onClose func(peek []byte)
	closed  bool
}

func (p *modelRespBodyProbe) Read(b []byte) (int, error) {
	n, err := p.rc.Read(b)
	if n > 0 {
		if !p.stream {
			remain := modelLogRespPeekLimit - p.buf.Len()
			if remain > 0 {
				take := n
				if take > remain {
					take = remain
				}
				_, _ = p.buf.Write(b[:take])
			}
		} else {
			data := append(p.buf.Bytes(), b[:n]...)
			if len(data) > modelLogRespPeekLimit {
				data = data[len(data)-modelLogRespPeekLimit:]
			}
			p.buf.Reset()
			_, _ = p.buf.Write(data)
		}
	}
	return n, err
}

func (p *modelRespBodyProbe) Close() error {
	err := p.rc.Close()
	if !p.closed {
		p.closed = true
		if p.onClose != nil {
			p.onClose(p.buf.Bytes())
		}
	}
	return err
}

func redactURL(u string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i] + "?…"
	}
	return u
}

// readAndSummarizeModelRequest 完整读取请求体后还原给下游，并生成不含密钥的摘要。
func readAndSummarizeModelRequest(req *http.Request) string {
	if req == nil || req.Body == nil {
		return "body=empty"
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		req.Body = io.NopCloser(bytes.NewReader(nil))
		return "body_read_err=" + err.Error()
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return buildReqSummaryFromBody(body)
}

func buildReqSummaryFromBody(body []byte) string {
	var parts []string
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return "raw_len=" + strconv.Itoa(len(body))
	}
	if v, ok := payload["model"]; ok {
		parts = append(parts, "model="+trimJSONString(v))
	}
	if v, ok := payload["max_tokens"]; ok {
		parts = append(parts, "max_tokens="+strings.TrimSpace(string(v)))
	}
	if v, ok := payload["stream"]; ok {
		parts = append(parts, "stream="+strings.TrimSpace(string(v)))
	}
	if v, ok := payload["temperature"]; ok {
		parts = append(parts, "temperature="+strings.TrimSpace(string(v)))
	}
	if v, ok := payload["enable_thinking"]; ok {
		parts = append(parts, "enable_thinking="+strings.TrimSpace(string(v)))
	}
	if v, ok := payload["messages"]; ok {
		var msgs []json.RawMessage
		if json.Unmarshal(v, &msgs) == nil {
			parts = append(parts, "messages="+strconv.Itoa(len(msgs)))
			parts = append(parts, "messages_bytes="+strconv.Itoa(len(v)))
		}
	}
	if v, ok := payload["tools"]; ok {
		var tools []json.RawMessage
		if json.Unmarshal(v, &tools) == nil {
			parts = append(parts, "tools="+strconv.Itoa(len(tools)))
		}
	}
	parts = append(parts, "body_len="+strconv.Itoa(len(body)))
	return strings.Join(parts, " ")
}

func summarizeModelResponseMeta(peek []byte) string {
	if len(peek) == 0 {
		return "peek=empty"
	}
	text := string(peek)
	var parts []string

	if finish := findLastJSONField(text, "finish_reason"); finish != "" {
		parts = append(parts, "finish_reason="+finish)
	}
	if u := findLastUsage(text); u != "" {
		parts = append(parts, "usage={"+u+"}")
	}
	if strings.Contains(strings.ToLower(text), "error") {
		if e := findLastJSONField(text, "message"); e != "" {
			parts = append(parts, "error_message="+truncateRunes(e, 200))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "peek_len="+strconv.Itoa(len(peek)))
	}
	return strings.Join(parts, " ")
}

func findLastJSONField(text, field string) string {
	key := `"` + field + `"`
	idx := strings.LastIndex(text, key)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(key):]
	rest = strings.TrimLeft(rest, " \t\r\n:")
	if rest == "" {
		return ""
	}
	if rest[0] == '"' {
		var b strings.Builder
		for i := 1; i < len(rest); i++ {
			if rest[i] == '\\' && i+1 < len(rest) {
				b.WriteByte(rest[i+1])
				i++
				continue
			}
			if rest[i] == '"' {
				return b.String()
			}
			b.WriteByte(rest[i])
		}
		return ""
	}
	end := 0
	for end < len(rest) {
		c := rest[end]
		if c == ',' || c == '}' || c == ']' || c == '\n' || c == '\r' || c == ' ' {
			break
		}
		end++
	}
	return strings.TrimSpace(rest[:end])
}

func findLastUsage(text string) string {
	idx := strings.LastIndex(text, `"usage"`)
	if idx < 0 {
		return ""
	}
	rest := text[idx:]
	if len(rest) > 400 {
		rest = rest[:400]
	}
	var parts []string
	if prompt := findLastJSONField(rest, "prompt_tokens"); prompt != "" {
		parts = append(parts, "prompt="+prompt)
	}
	if completion := findLastJSONField(rest, "completion_tokens"); completion != "" {
		parts = append(parts, "completion="+completion)
	}
	if total := findLastJSONField(rest, "total_tokens"); total != "" {
		parts = append(parts, "total="+total)
	}
	return strings.Join(parts, " ")
}

func trimJSONString(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	return strings.Trim(s, `"`)
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
