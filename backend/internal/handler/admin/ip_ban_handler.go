package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// IPBanHandler 管理 IP / 网段封禁。
type IPBanHandler struct {
	ipBanService *service.IPBanService
}

func NewIPBanHandler(ipBanService *service.IPBanService) *IPBanHandler {
	return &IPBanHandler{ipBanService: ipBanService}
}

type ipBanResponse struct {
	ID        int64     `json:"id"`
	IP        string    `json:"ip"`
	Reason    string    `json:"reason"`
	CreatedBy *int64    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

func ipBanToResponse(ban *service.IPBan) *ipBanResponse {
	if ban == nil {
		return nil
	}
	return &ipBanResponse{
		ID:        ban.ID,
		IP:        ban.IP,
		Reason:    ban.Reason,
		CreatedBy: ban.CreatedBy,
		CreatedAt: ban.CreatedAt,
	}
}

type createIPBanRequest struct {
	IP     string `json:"ip" binding:"required"`
	Reason string `json:"reason"`
}

type createIPBanResponse struct {
	Ban     *ipBanResponse `json:"ban"`
	Created bool           `json:"created"` // false 表示该 IP / 网段此前已被封禁
}

type ipInspectionResponse struct {
	IP                string         `json:"ip"`
	NetworkKey        string         `json:"network_key"` // 注册额度计数单位，也是建议的封禁目标
	Banned            bool           `json:"banned"`
	Ban               *ipBanResponse `json:"ban"`
	RegisteredTotal   int            `json:"registered_total"`
	RegisteredToday   int            `json:"registered_today"`
	Bannable          bool           `json:"bannable"`
	NotBannableReason string         `json:"not_bannable_reason,omitempty"`
}

// List 分页列出封禁规则
// GET /api/v1/admin/ip-bans?page=&page_size=&search=
func (h *IPBanHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	search := strings.TrimSpace(c.Query("search"))
	if runes := []rune(search); len(runes) > 64 {
		search = string(runes[:64])
	}
	bans, result, err := h.ipBanService.List(c.Request.Context(), page, pageSize, search)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]*ipBanResponse, 0, len(bans))
	for i := range bans {
		out = append(out, ipBanToResponse(&bans[i]))
	}
	var total int64
	if result != nil {
		total = result.Total
	}
	response.Paginated(c, out, total, page, pageSize)
}

// Create 封禁一个 IP 或网段（幂等：已封禁时返回现有规则）
// POST /api/v1/admin/ip-bans
func (h *IPBanHandler) Create(c *gin.Context) {
	var req createIPBanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	var actorID int64
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		actorID = subject.UserID
	}
	ban, created, err := h.ipBanService.Ban(c.Request.Context(), req.IP, req.Reason, actorID, middleware.SecurityClientIP(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, createIPBanResponse{Ban: ipBanToResponse(ban), Created: created})
}

// Delete 解除封禁
// DELETE /api/v1/admin/ip-bans/:id
func (h *IPBanHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid IP ban ID")
		return
	}
	if err := h.ipBanService.Unban(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "IP ban removed"})
}

// Inspect 查看某个 IP 的封禁状态与注册情况
// GET /api/v1/admin/ip-bans/inspect?ip=
func (h *IPBanHandler) Inspect(c *gin.Context) {
	inspection, err := h.ipBanService.Inspect(c.Request.Context(), c.Query("ip"), middleware.SecurityClientIP(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, ipInspectionResponse{
		IP:                inspection.IP,
		NetworkKey:        inspection.NetworkKey,
		Banned:            inspection.Ban != nil,
		Ban:               ipBanToResponse(inspection.Ban),
		RegisteredTotal:   inspection.RegisteredTotal,
		RegisteredToday:   inspection.RegisteredToday,
		Bannable:          inspection.Bannable,
		NotBannableReason: inspection.NotBannableReason,
	})
}
