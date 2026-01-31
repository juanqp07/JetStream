package subsonic

const (
	Version      = "1.16.1"
	StatusOk     = "ok"
	StatusFailed = "failed"
)

// Subsonic Error Codes
const (
	ErrGeneric           = 0
	ErrRequiredParameter = 10
	ErrClientVersionOld  = 20
	ErrServerVersionOld  = 30
	ErrWrongUserPass     = 40
	ErrNotAuthorized     = 50
	ErrTrialExpired      = 60
	ErrDataNotFound      = 70
	ErrUserNotAuthorized = 80
	ErrArtistNotFound    = 70 // Re-using 70 according to some specs or Navidrome behavior
)

// SendSubsonicResponse and SendSubsonicError should probably be here too if we want to decoupling from Gin,
// but they depend on gin.Context, so maybe in a handlers/utils.go or subsonic/gin_utils.go
