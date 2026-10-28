package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *UserHandler) RequestAffiliateWithdrawal(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req struct {
		Amount  float64 `json:"amount" binding:"required"`
		Account string  `json:"alipay_account" binding:"required"`
		Name    string  `json:"alipay_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请填写提现金额、支付宝账号和实名姓名")
		return
	}
	record, err := h.affiliateService.RequestWithdrawal(c.Request.Context(), subject.UserID, req.Amount, req.Account, req.Name)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, record)
}

func (h *UserHandler) ListAffiliateWalletRecords(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	page, size := response.ParsePagination(c)
	items, total, err := h.affiliateService.WalletRecords(c.Request.Context(), subject.UserID, c.DefaultQuery("kind", "ledger"), page, size, false)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, items, total, page, size)
}
