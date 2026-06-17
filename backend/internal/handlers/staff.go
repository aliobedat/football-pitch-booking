package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

// StaffHandler serves the owner-scoped staff provisioning surface. An owner
// invites a guard by phone and binds them to a pitch they own. The strict
// ownership invariant (owner must own the target pitch) is enforced in the
// repository's transaction; the handler maps its sentinel errors to HTTP codes.
type StaffHandler struct {
	repo repository.StaffRepository
}

// NewStaffHandler constructs a StaffHandler.
func NewStaffHandler(repo repository.StaffRepository) *StaffHandler {
	return &StaffHandler{repo: repo}
}

type inviteStaffRequest struct {
	Phone string `json:"phone"`
}

// InviteStaff provisions a staff member for a pitch the owner owns.
// POST /pitches/:id/staff  body: { "phone": "+9627XXXXXXXX" }
//
// Route is RequireRole("owner","admin"). The owner→pitch ownership check lives in
// the repository (single transaction with the promotion+binding), so an owner can
// only ever bind staff to a pitch_id they actually own.
func (h *StaffHandler) InviteStaff(c *gin.Context) {
	pitchID, err := strconv.Atoi(c.Param("id"))
	if err != nil || pitchID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_pitch_id", "message": "invalid pitch id"})
		return
	}

	var req inviteStaffRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad_request", "message": "صيغة الطلب غير صحيحة"})
		return
	}

	phone, err := normalizePhone(req.Phone)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_phone", "message": err.Error()})
		return
	}

	// The owner is the authenticated actor. An admin acting on a pitch they do not
	// own would also be rejected by the ownership check — staff are owner-bound by
	// design, so we always scope the binding to the acting owner's id.
	ownerID := middleware.GetUserID(c)

	binding, err := h.repo.CreateStaffBinding(c.Request.Context(), ownerID, pitchID, phone)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrPitchNotOwned):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "not_pitch_owner", "message": "you can only assign staff to a pitch you own",
			})
		case errors.Is(err, repository.ErrStaffUserNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "staff_user_not_found", "message": "this phone has not registered yet — ask them to log in once first",
			})
		case errors.Is(err, repository.ErrStaffAlreadyBound):
			c.JSON(http.StatusConflict, gin.H{
				"error": "staff_already_assigned", "message": "this user is already assigned to a pitch",
			})
		case errors.Is(err, repository.ErrCannotBindPrivileged):
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "cannot_assign_user", "message": "this user cannot be assigned as staff",
			})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal_error", "message": "could not assign staff, please try again",
			})
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": binding})
}

// RevokeStaff removes a staff member the owner provisioned: the binding is deleted
// and the user demoted back to `player`, atomically. Owner-scoped — an owner can
// only revoke their OWN staff (the repository's owner_id predicate enforces it).
// DELETE /owner/staff/:userId
func (h *StaffHandler) RevokeStaff(c *gin.Context) {
	staffUserID, err := strconv.Atoi(c.Param("userId"))
	if err != nil || staffUserID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_user_id", "message": "معرّف المستخدم غير صالح"})
		return
	}

	ownerID := middleware.GetUserID(c)
	if err := h.repo.RevokeStaff(c.Request.Context(), ownerID, staffUserID); err != nil {
		switch {
		case errors.Is(err, repository.ErrStaffBindingNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "staff_not_found", "message": "لا يوجد موظف بهذا المعرّف ضمن حسابك",
			})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal_error", "message": "تعذّر تسريح الموظف، حاول مرة أخرى",
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "تم تسريح الموظف"})
}

// ListStaff returns the staff members the authenticated owner has provisioned.
// GET /owner/staff
func (h *StaffHandler) ListStaff(c *gin.Context) {
	ownerID := middleware.GetUserID(c)
	staff, err := h.repo.ListStaffForOwner(c.Request.Context(), ownerID)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "could not load staff"})
		return
	}
	if staff == nil {
		staff = []repository.StaffBinding{}
	}
	c.JSON(http.StatusOK, gin.H{"data": staff})
}
