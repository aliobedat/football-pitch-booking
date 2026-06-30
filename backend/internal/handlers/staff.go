package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

// StaffHandler serves the owner-scoped staff provisioning surface. An owner invites
// a guard by phone and binds them to ONE OR MORE pitches they own (1:N). The strict
// ownership invariant (owner must own every target pitch) is enforced in the
// repository's transaction; the handler maps its sentinel errors to HTTP codes.
type StaffHandler struct {
	repo       repository.StaffRepository
	bcryptCost int
}

// NewStaffHandler constructs a StaffHandler. bcryptCost (cfg.BcryptCost) is used to
// hash an onboarding password with the SAME bcrypt path as dbadmin -set-password.
func NewStaffHandler(repo repository.StaffRepository, bcryptCost int) *StaffHandler {
	return &StaffHandler{repo: repo, bcryptCost: bcryptCost}
}

// minStaffPasswordLen mirrors the provisioning minimum used by dbadmin.
const minStaffPasswordLen = 8

type inviteStaffRequest struct {
	Phone    string `json:"phone"`
	PitchIDs []int  `json:"pitch_ids"`
	// FullName is saved/updated when provided (optional).
	FullName string `json:"full_name"`
	// Password is optional in general but REQUIRED for a brand-new user or a player
	// with no existing password (the backend enforces this and returns 422). When
	// present it sets/resets the user's password_hash.
	Password string `json:"password"`
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

	// Build the provision: hash the password (if any) with the configured cost via
	// the shared bcrypt helper — the repository never sees plaintext. A too-short
	// password is rejected here (an empty password is allowed through; the repo
	// decides whether this onboarding case actually requires one).
	prov := repository.StaffProvision{FullName: strings.TrimSpace(req.FullName)}
	if pw := req.Password; pw != "" {
		if len(pw) < minStaffPasswordLen {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "weak_password", "message": "كلمة المرور يجب أن تكون 8 أحرف على الأقل",
			})
			return
		}
		hash, herr := auth.HashPassword(pw, h.bcryptCost)
		if herr != nil {
			c.Error(herr)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal_error", "message": "تعذّر تعيين الموظف، حاول مرة أخرى",
			})
			return
		}
		prov.PasswordHash = hash
	}

	// Authorization is resolved in the repository from the actor: an owner is scoped
	// to pitches they own; an admin may bind to any live pitch (the binding's
	// owner_id is resolved to the pitch's real owner). The route guard already bars
	// staff/player.
	actor := middleware.GetActor(c)

	member, err := h.repo.CreateStaffBindings(c.Request.Context(), actor, req.PitchIDs, phone, prov)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrPitchNotOwned):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "not_pitch_owner", "message": "يمكنك تعيين موظف على ملاعبك فقط",
			})
		case errors.Is(err, repository.ErrStaffForeignOwner):
			c.JSON(http.StatusForbidden, gin.H{
				"error": "staff_foreign_owner", "message": "هذا الموظف مُسجَّل لدى مالك آخر",
			})
		case errors.Is(err, repository.ErrPasswordRequired):
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "password_required", "message": "كلمة المرور مطلوبة لإضافة هذا الموظف",
			})
		case errors.Is(err, repository.ErrCannotBindPrivileged):
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "cannot_assign_user", "message": "لا يمكن إضافة هذا الرقم كموظف",
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

	actor := middleware.GetActor(c)
	if err := h.repo.RevokeStaff(c.Request.Context(), actor, staffUserID); err != nil {
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

// ListStaff returns provisioned staff: an owner's own, or — for an admin — every
// owner's staff. Scope is resolved in the repository from the actor.
// GET /owner/staff
func (h *StaffHandler) ListStaff(c *gin.Context) {
	actor := middleware.GetActor(c)
	staff, err := h.repo.ListStaff(c.Request.Context(), actor)
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
