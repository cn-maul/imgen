package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/imgen/imgen/internal/domain"
)

// maxJSONBody 是 JSON 请求体的上限。任何合法请求都远小于此。
const maxJSONBody = 1 << 20 // 1MB

// errorResponse 是统一错误体：{"error": {...}}。
// 内层原样是 domain.Error 的 JSON，retryable 必须透出 —— 前端据此决定
// 弹「重试」还是「改提示词」。
type errorResponse struct {
	Error *domain.Error `json:"error"`
}

// statusForCode 把归一化错误码映射为 HTTP 状态码。
//
// 注意：UPSTREAM_REJECTED 按约定走 502（上游返回了我们无法处理的响应），
// 而不是 400 —— 本服务看来请求本身是合法的。
func statusForCode(code string) int {
	switch code {
	case domain.CodeUpstreamAuth:
		return http.StatusUnauthorized
	case domain.CodeUpstreamQuota:
		return http.StatusTooManyRequests
	case domain.CodeUpstreamTimeout:
		return http.StatusGatewayTimeout
	case domain.CodeUpstreamRejected,
		domain.CodeUpstreamUnavailable,
		domain.CodeUpstreamUnparseable:
		return http.StatusBadGateway
	case domain.CodeBadRequest:
		return http.StatusBadRequest
	case domain.CodeNotFound:
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// statusFor 由错误码推导出 HTTP 状态码。
func statusFor(e *domain.Error) int {
	if e == nil {
		return http.StatusInternalServerError
	}
	return statusForCode(e.Code)
}

// writeDomainErr 直接写出归一化错误（含 retryable）。
func writeDomainErr(w http.ResponseWriter, e *domain.Error) {
	if e == nil {
		e = domain.NewError(domain.CodeInternal, "未知错误")
	}
	if e.Code == "" {
		e.Code = domain.CodeInternal
	}
	writeJSON(w, statusFor(e), errorResponse{Error: e})
}

// writeErr 把任意 error 收敛为统一错误体后写出。
//
// 判别一律走 errors.Is / errors.As，不做字符串匹配：
// 仓储与方法都用 fmt.Errorf("%w: ...") 包装了哨兵错误。
func writeErr(w http.ResponseWriter, err error) {
	writeDomainErr(w, toDomainError(err))
}

// toDomainError 把任意 error 收敛为 *domain.Error。
func toDomainError(err error) *domain.Error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if errors.As(err, &de) {
		if de.Code == "" {
			de.Code = domain.CodeInternal
		}
		return de
	}

	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.NewError(domain.CodeNotFound, err.Error())
	case errors.Is(err, domain.ErrBadRequest):
		return domain.NewError(domain.CodeBadRequest, err.Error())
	case errors.Is(err, domain.ErrNoCredential):
		// 未配置凭据属于用户可自行修复的问题，走 400 并提示去设置页配 Key。
		return domain.NewError(domain.CodeBadRequest, err.Error())
	case errors.Is(err, context.Canceled):
		return domain.NewError(domain.CodeInternal, "请求已取消")
	case errors.Is(err, context.DeadlineExceeded):
		return &domain.Error{Code: domain.CodeUpstreamTimeout, Message: "处理超时", Retryable: true}
	default:
		return domain.NewError(domain.CodeInternal, err.Error())
	}
}

// 语义化抛错构造器，避免各处手写消息与错误码。
func errNotFound(msg string) *domain.Error {
	return domain.NewError(domain.CodeNotFound, msg)
}

func errBadRequest(msg string) *domain.Error {
	return domain.NewError(domain.CodeBadRequest, msg)
}

func errInternal(msg string, err error) *domain.Error {
	if err == nil {
		return domain.NewError(domain.CodeInternal, msg)
	}
	return domain.NewError(domain.CodeInternal, msg+": "+err.Error())
}

// writeJSON 写出 JSON；序列化失败时退化为纯文本 500，避免写出半个响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("序列化响应失败"))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// writeNoContent 用于 DELETE 等无响应体的成功语义。
func writeNoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// decodeJSON 解析请求体。未知字段忽略：前端多发字段不应造成 400，
// 否则前后端版本会形成无谓的强耦合。
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxJSONBody+1))
	if err != nil {
		writeErr(w, err)
		return false
	}
	if len(raw) > maxJSONBody {
		writeDomainErr(w, errBadRequest("请求体过大"))
		return false
	}
	if len(raw) == 0 {
		writeDomainErr(w, errBadRequest("请求体为空"))
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		writeDomainErr(w, errBadRequest("请求体不是合法 JSON: "+err.Error()))
		return false
	}
	return true
}
