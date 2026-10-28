package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"strconv"
)

func (h *AffiliateHandler) ListWithdrawals(c *gin.Context) {
	page, size := response.ParsePagination(c)
	items, total, err := h.affiliateService.WalletRecords(c.Request.Context(), 0, "withdrawals", page, size, true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, size)
}
func (h *AffiliateHandler) ProcessWithdrawal(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid id")
		return
	}
	var req struct {
		Status string `json:"status"`
		Note   string `json:"admin_note"`
	}
	if err = c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	if err = h.affiliateService.ProcessWithdrawal(c.Request.Context(), id, subject.UserID, req.Status, req.Note); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "status": req.Status})
}
func (h *AffiliateHandler) GetWalletDetail(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid user_id")
		return
	}
	detail, err := h.affiliateService.GetAffiliateDetail(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, detail)
}
func (h *AffiliateHandler) ListWalletRecords(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid user_id")
		return
	}
	page, size := response.ParsePagination(c)
	items, total, err := h.affiliateService.WalletRecords(c.Request.Context(), id, c.DefaultQuery("kind", "ledger"), page, size, true)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, size)
}
