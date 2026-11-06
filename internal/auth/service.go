package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pestit/retain/internal/clock"
	"github.com/pestit/retain/internal/db"
	"github.com/pestit/retain/internal/db/gen"
)

const (
	inviteLifetime    = 7 * 24 * time.Hour
	sessionLifetime   = 30 * 24 * time.Hour
	maxNameRunes      = 100
	maxEmailBytes     = 254
	maxUserAgentBytes = 512
)

var (
	// ErrInvalidInput means an account or settings value is invalid.
	ErrInvalidInput = errors.New("invalid input")
	// ErrInvalidInvite means an invite does not exist or is otherwise invalid.
	ErrInvalidInvite = errors.New("invalid invite")
	// ErrInviteExpired means an invite has expired.
	ErrInviteExpired = errors.New("invite expired")
	// ErrInviteUsed means an invite has already been consumed.
	ErrInviteUsed = errors.New("invite already used")
	// ErrEmailExists means an account already uses the normalized email address.
	ErrEmailExists = errors.New("email already exists")
	// ErrInvalidCredentials intentionally covers unknown users and wrong passwords.
	ErrInvalidCredentials = errors.New("invalid email or password")
	// ErrUnauthenticated means the request has no valid authenticated account.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrForbidden means the authenticated user is not allowed to perform the operation.
	ErrForbidden = errors.New("forbidden")
	// ErrInternal is a sanitized operational error. Database and secret-bearing details
	// are deliberately not included in its message.
	ErrInternal = errors.New("internal auth service error")
)

// User is the public profile returned by account and authentication operations.
// It deliberately excludes the password hash.
type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

// Settings contains all user_settings columns. FsrsWeights is nil when the
// database value is NULL, and ReminderTime is nil when no reminder is set.
type Settings struct {
	UserID           int64           `json:"user_id"`
	NewPerDay        int64           `json:"new_per_day"`
	MaxReviewsPerDay int64           `json:"max_reviews_per_day"`
	RequestRetention float64         `json:"request_retention"`
	MaximumInterval  int64           `json:"maximum_interval"`
	LearningSteps    string          `json:"learning_steps"`
	RelearningSteps  string          `json:"relearning_steps"`
	FsrsWeights      json.RawMessage `json:"fsrs_weights"`
	Timezone         string          `json:"timezone"`
	DayStartHour     int64           `json:"day_start_hour"`
	ReminderTime     *string         `json:"reminder_time"`
	TtsAuto          bool            `json:"tts_auto"`
	ThemeMode        string          `json:"theme_mode"`
	ThemeSeed        string          `json:"theme_seed"`
	ThemeVariant     string          `json:"theme_variant"`
	ThemeContrast    float64         `json:"theme_contrast"`
}

// Account pairs a public user profile with the user's complete settings.
type Account struct {
	User     User     `json:"user"`
	Settings Settings `json:"settings"`
}

// RegisterInput contains invite-based account registration fields.
type RegisterInput struct {
	Invite   string `json:"invite"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// LoginInput contains credentials for a new session.
type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResult contains the raw opaque token only so the HTTP handler can set
// the session cookie. Never log Token.
type LoginResult struct {
	User      User      `json:"user"`
	Token     string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Invite is a single-use invite code and its expiration time.
type Invite struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Service implements account, invite, settings, and cookie-session business logic.
type Service struct {
	database       *db.DB
	clock          clock.Clock
	hashPassword   func(string) (string, error)
	verifyPassword func(string, string) (bool, error)
}

// NewService creates an auth service backed by database and the provided clock.
func NewService(database *db.DB, c clock.Clock) *Service {
	if c == nil {
		c = clock.Real{}
	}
	return &Service{
		database:       database,
		clock:          c,
		hashPassword:   HashPassword,
		verifyPassword: VerifyPassword,
	}
}

// Register creates a user and default settings while consuming a single-use
// invite in the same transaction. Password hashing happens before the transaction.
func (s *Service) Register(ctx context.Context, input RegisterInput) (User, error) {
	if !s.ready() {
		return User{}, ErrInternal
	}
	values, err := normalizeRegistration(input)
	if err != nil {
		return User{}, err
	}

	now := s.now()
	if _, err := getValidInvite(ctx, s.database.Queries(), values.invite, now); err != nil {
		return User{}, err
	}
	if err := s.ensureEmailAvailable(ctx, s.database.Queries(), values.email); err != nil {
		return User{}, err
	}

	passwordHash, err := s.hashPassword(input.Password)
	if errors.Is(err, ErrPasswordHashBusy) {
		return User{}, ErrPasswordHashBusy
	}
	if err != nil {
		return User{}, ErrInternal
	}
	now = s.now()

	tx, queries, err := s.begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.ensureEmailAvailable(ctx, queries, values.email); err != nil {
		return User{}, err
	}
	created, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email:        values.email,
		Name:         values.name,
		PasswordHash: passwordHash,
		CreatedAt:    now,
	})
	if err != nil {
		if errors.Is(s.ensureEmailAvailable(ctx, queries, values.email), ErrEmailExists) {
			return User{}, ErrEmailExists
		}
		return User{}, ErrInternal
	}
	if _, err := queries.CreateUserSettings(ctx, created.ID); err != nil {
		return User{}, ErrInternal
	}
	consumeAt := s.now()
	if _, err := queries.ConsumeInvite(ctx, gen.ConsumeInviteParams{
		UsedBy: created.ID,
		Code:   values.invite,
		Now:    consumeAt,
	}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, inviteStateError(ctx, queries, values.invite, consumeAt)
		}
		return User{}, ErrInternal
	}
	if err := tx.Commit(); err != nil {
		return User{}, ErrInternal
	}
	return publicUser(created), nil
}

// Login verifies credentials, creates a 30-day session, and returns its raw
// token for the caller to place in a secure cookie.
func (s *Service) Login(ctx context.Context, input LoginInput, userAgent string) (LoginResult, error) {
	if !s.ready() {
		return LoginResult{}, ErrInternal
	}
	email, ok := normalizeEmail(input.Email)
	if !ok || !utf8.ValidString(input.Password) {
		return LoginResult{}, ErrInvalidCredentials
	}

	user, err := s.database.Queries().GetUserByEmail(ctx, email)
	if errors.Is(err, sql.ErrNoRows) {
		_, verifyErr := s.verifyPassword(input.Password, dummyPasswordHash)
		if errors.Is(verifyErr, ErrPasswordHashBusy) {
			return LoginResult{}, ErrPasswordHashBusy
		}
		if verifyErr != nil {
			return LoginResult{}, ErrInternal
		}
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, ErrInternal
	}

	matched, err := s.verifyPassword(input.Password, user.PasswordHash)
	if errors.Is(err, ErrPasswordHashBusy) {
		return LoginResult{}, ErrPasswordHashBusy
	}
	if err != nil || !matched {
		return LoginResult{}, ErrInvalidCredentials
	}

	now := s.now()
	if _, err := s.database.Queries().DeleteExpiredSessions(ctx, now); err != nil {
		return LoginResult{}, ErrInternal
	}
	token, err := GenerateSessionToken()
	if err != nil {
		return LoginResult{}, ErrInternal
	}
	expiresAt := now.Add(sessionLifetime)
	_, err = s.database.Queries().CreateSession(ctx, gen.CreateSessionParams{
		TokenHash: sessionTokenDigest(token),
		UserID:    user.ID,
		ExpiresAt: expiresAt,
		UserAgent: nullableUserAgent(userAgent),
		CreatedAt: now,
	})
	if err != nil {
		return LoginResult{}, ErrInternal
	}
	return LoginResult{User: publicUser(user), Token: token, ExpiresAt: expiresAt}, nil
}

// Logout deletes the session identified by token. Unknown and malformed tokens
// are intentionally treated as successful no-ops.
func (s *Service) Logout(ctx context.Context, token string) error {
	if !s.ready() {
		return ErrInternal
	}
	if !validSessionToken(token) {
		return nil
	}
	if _, err := s.database.Queries().DeleteSession(ctx, sessionTokenDigest(token)); err != nil {
		return ErrInternal
	}
	return nil
}

// Authenticate resolves a raw session token to its public user. Expired rows
// are removed before the unauthenticated result is returned.
func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	if !s.ready() {
		return User{}, ErrInternal
	}
	if !validSessionToken(token) {
		return User{}, ErrUnauthenticated
	}
	digest := sessionTokenDigest(token)
	queries := s.database.Queries()
	session, err := queries.GetSession(ctx, digest)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, ErrInternal
	}
	now := s.now()
	if !session.ExpiresAt.After(now) {
		if _, err := queries.DeleteSession(ctx, digest); err != nil {
			return User{}, ErrInternal
		}
		return User{}, ErrUnauthenticated
	}
	user, err := queries.GetUserByID(ctx, session.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, ErrInternal
	}
	return publicUser(user), nil
}

// CreateInvite creates a seven-day invite only for an existing administrator.
func (s *Service) CreateInvite(ctx context.Context, adminID int64) (Invite, error) {
	if !s.ready() {
		return Invite{}, ErrInternal
	}
	if adminID <= 0 {
		return Invite{}, ErrForbidden
	}
	admin, err := s.database.Queries().GetUserByID(ctx, adminID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !admin.IsAdmin) {
		return Invite{}, ErrForbidden
	}
	if err != nil {
		return Invite{}, ErrInternal
	}
	code, err := GenerateInviteCode()
	if err != nil {
		return Invite{}, ErrInternal
	}
	now := s.now()
	expiresAt := now.Add(inviteLifetime)
	if _, err := s.database.Queries().CreateInvite(ctx, gen.CreateInviteParams{
		Code:      code,
		CreatedBy: adminID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}); err != nil {
		return Invite{}, ErrInternal
	}
	return Invite{Code: code, ExpiresAt: expiresAt}, nil
}

// CreateAdmin creates an administrator and default settings atomically. It is
// intended for the trusted CLI bootstrap path, not an HTTP endpoint.
func (s *Service) CreateAdmin(ctx context.Context, email, name, password string) (int64, error) {
	if !s.ready() {
		return 0, ErrInternal
	}
	values, err := normalizeAccount(email, name, password)
	if err != nil {
		return 0, err
	}
	if err := s.ensureEmailAvailable(ctx, s.database.Queries(), values.email); err != nil {
		return 0, err
	}
	passwordHash, err := s.hashPassword(password)
	if errors.Is(err, ErrPasswordHashBusy) {
		return 0, ErrPasswordHashBusy
	}
	if err != nil {
		return 0, ErrInternal
	}

	tx, queries, err := s.begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.ensureEmailAvailable(ctx, queries, values.email); err != nil {
		return 0, err
	}
	user, err := queries.CreateUser(ctx, gen.CreateUserParams{
		Email:        values.email,
		Name:         values.name,
		PasswordHash: passwordHash,
		IsAdmin:      true,
		CreatedAt:    s.now(),
	})
	if err != nil {
		if errors.Is(s.ensureEmailAvailable(ctx, queries, values.email), ErrEmailExists) {
			return 0, ErrEmailExists
		}
		return 0, ErrInternal
	}
	if _, err := queries.CreateUserSettings(ctx, user.ID); err != nil {
		return 0, ErrInternal
	}
	if err := tx.Commit(); err != nil {
		return 0, ErrInternal
	}
	return user.ID, nil
}

// GetAccount returns a user's public profile and complete settings.
func (s *Service) GetAccount(ctx context.Context, userID int64) (Account, error) {
	if !s.ready() {
		return Account{}, ErrInternal
	}
	user, err := s.database.Queries().GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrUnauthenticated
	}
	if err != nil {
		return Account{}, ErrInternal
	}
	settings, err := s.database.Queries().GetUserSettings(ctx, userID)
	if err != nil {
		return Account{}, ErrInternal
	}
	return Account{User: publicUser(user), Settings: publicSettings(settings)}, nil
}

type normalizedAccount struct {
	email  string
	name   string
	invite string
}

func normalizeRegistration(input RegisterInput) (normalizedAccount, error) {
	values, err := normalizeAccount(input.Email, input.Name, input.Password)
	if err != nil {
		return normalizedAccount{}, err
	}
	invite := strings.TrimSpace(input.Invite)
	if invite == "" || !utf8.ValidString(invite) {
		return normalizedAccount{}, ErrInvalidInvite
	}
	values.invite = invite
	return values, nil
}

func normalizeAccount(email, name, password string) (normalizedAccount, error) {
	normalizedEmail, ok := normalizeEmail(email)
	if !ok {
		return normalizedAccount{}, ErrInvalidInput
	}
	normalizedName, err := NormalizeDisplayName(name)
	if err != nil {
		return normalizedAccount{}, ErrInvalidInput
	}
	if err := ValidatePassword(password); err != nil {
		return normalizedAccount{}, ErrInvalidInput
	}
	return normalizedAccount{email: normalizedEmail, name: normalizedName}, nil
}

func normalizeEmail(email string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(email) > maxEmailBytes || !utf8.ValidString(email) {
		return "", false
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Name != "" || address.Address != email {
		return "", false
	}
	return email, true
}

// NormalizeDisplayName trims and validates an account display name.
func NormalizeDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return "", ErrInvalidInput
	}
	return name, nil
}

func validName(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxNameRunes {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (s *Service) ensureEmailAvailable(ctx context.Context, queries *gen.Queries, email string) error {
	_, err := queries.GetUserByEmail(ctx, email)
	switch {
	case err == nil:
		return ErrEmailExists
	case errors.Is(err, sql.ErrNoRows):
		return nil
	default:
		return ErrInternal
	}
}

func getValidInvite(ctx context.Context, queries *gen.Queries, code string, now time.Time) (gen.Invite, error) {
	invite, err := queries.GetInvite(ctx, code)
	if errors.Is(err, sql.ErrNoRows) {
		return gen.Invite{}, ErrInvalidInvite
	}
	if err != nil {
		return gen.Invite{}, ErrInternal
	}
	if invite.UsedBy.Valid {
		return gen.Invite{}, ErrInviteUsed
	}
	if !invite.ExpiresAt.After(now) {
		return gen.Invite{}, ErrInviteExpired
	}
	return invite, nil
}

func inviteStateError(ctx context.Context, queries *gen.Queries, code string, now time.Time) error {
	_, err := getValidInvite(ctx, queries, code, now)
	if err != nil {
		return err
	}
	return ErrInvalidInvite
}

func (s *Service) begin(ctx context.Context) (*sql.Tx, *gen.Queries, error) {
	tx, err := s.database.SQL.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, ErrInternal
	}
	return tx, s.database.Queries().WithTx(tx), nil
}

func (s *Service) ready() bool {
	return s != nil && s.database != nil && s.database.SQL != nil && s.clock != nil &&
		s.hashPassword != nil && s.verifyPassword != nil
}

func (s *Service) now() time.Time {
	return s.clock.Now().UTC()
}

func publicUser(user gen.User) User {
	return User{
		ID:        user.ID,
		Email:     user.Email,
		Name:      user.Name,
		IsAdmin:   user.IsAdmin,
		CreatedAt: user.CreatedAt.UTC(),
	}
}

func publicSettings(settings gen.UserSetting) Settings {
	result := Settings{
		UserID:           settings.UserID,
		NewPerDay:        settings.NewPerDay,
		MaxReviewsPerDay: settings.MaxReviewsPerDay,
		RequestRetention: settings.RequestRetention,
		MaximumInterval:  settings.MaximumInterval,
		LearningSteps:    settings.LearningSteps,
		RelearningSteps:  settings.RelearningSteps,
		Timezone:         settings.Timezone,
		DayStartHour:     settings.DayStartHour,
		TtsAuto:          settings.TtsAuto,
		ThemeMode:        settings.ThemeMode,
		ThemeSeed:        settings.ThemeSeed,
		ThemeVariant:     settings.ThemeVariant,
		ThemeContrast:    settings.ThemeContrast,
	}
	if settings.FsrsWeights.Valid {
		result.FsrsWeights = json.RawMessage(settings.FsrsWeights.String)
	}
	if settings.ReminderTime.Valid {
		reminder := settings.ReminderTime.String
		result.ReminderTime = &reminder
	}
	return result
}

func sessionTokenDigest(token string) []byte {
	digest := HashSessionToken(token)
	return digest[:]
}

func validSessionToken(token string) bool {
	if len(token) != 43 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == sessionTokenSize && base64.RawURLEncoding.EncodeToString(decoded) == token
}

func nullableUserAgent(value string) sql.NullString {
	value = strings.ToValidUTF8(value, "")
	if len(value) > maxUserAgentBytes {
		value = value[:maxUserAgentBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

const dummyPasswordHash = "$argon2id$v=19$m=19456,t=2,p=1$cmV0YWluLWxvZ2luLWR1bW15$mKsKB4h1M/VmLtS+9CY/J5ufLRtlBabViT+jg4tWdZs"
