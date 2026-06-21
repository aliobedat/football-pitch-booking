package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

// StaffHandler serves the owner-scoped staff provisioning surface. An owner invites
// a guard by phone and binds them to ONE OR MORE pitches they own (1:N). The strict
// ownership invariant (owner must own every target pitch) is enforced in the
// repository's transaction; the handler maps its sentinel errors to HTTP codes.
type StaffHandler struct {
	repo repository.StaffRepository
}

// NewStaffHandler constructs a StaffHandler.
func NewStaffHandler(repo repository.StaffRepository) *StaffHandler {
	return &StaffHandler{repo: repo}
}

type inviteStaffRequest struct {
	Phone    string `json:"phone"`
	PitchIDs []int  `json:"pitch_ids"`
}

// InviteStaff provisions a staff member across one or more owned pitches (1:N).
// POST /owner/staff  body: { "phone": "+9627…", "pitch_ids": [12, 15] }
//
// Route is RequireRole("owner","admin"). The owner→pitch ownership check lives in
// the repository (single transaction with the promotion+bindings), so an owner can
// only ever bind staff to pitch_ids they actually own. Re-inviting to add pitches
// is idempotent.
func (h *StaffHandler) InviteStaff(c *gin.Context) {
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

	// Validate the pitch set: at least one, all positive ids.
	if len(req.PitchIDs) == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "no_pitches", "message": "اختر ملعباً واحداً على الأقل",
		})
		return
	}
	for _, pid := range req.PitchIDs {
		if pid <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_pitch_id", "message": "معرّف ملعب غير صالح"})
			return
		}
	}

	// The owner is the authenticated actor. An admin acting on pitches they do not
	// own would also be rejected by the ownership check — staff are owner-bound by
	// design, so we always scope the binding to the acting owner's id.
	ownerID := middleware.GetUserID(c)

	member, err := h.repo.CreateStaffBindings(c.Request.Context(), ownerID, req.PitchIDs, phone)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrPitchNotOwned):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "not_pitch_owner", "message": "يمكنك تعيين موظف على ملاعبك فقط",
			})
		case errors.Is(err, repository.ErrStaffUserNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"error": "staff_user_not_found", "message": "هذا الرقم لم يسجّل بعد — اطلب من الموظف تسجيل الدخول مرة واحدة أولاً",
			})
		case errors.Is(err, repository.ErrCannotBindPrivileged):
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "cannot_assign_user", "message": "لا يمكن تعيين هذا المستخدم كموظف",
			})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal_error", "message": "تعذّر تعيين الموظف، حاول مرة أخرى",
			})
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": member})
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
		staff = []repository.StaffMember{}
	}
	c.JSON(http.StatusOK, gin.H{"data": staff})
}
