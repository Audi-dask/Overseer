package auth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	DefaultUsername = "admin"
	TokenTTL        = 7 * 24 * time.Hour
	Issuer          = "overseer"
)

var (
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrWeakPassword       = errors.New("密码至少 6 位")
	ErrSetupDone          = errors.New("管理员已初始化，请直接登录")
)

type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type Service struct {
	jwtKey []byte
}

func New(jwtKey []byte) *Service {
	if len(jwtKey) == 0 {
		raw := os.Getenv("MASTER_KEY")
		if raw == "" {
			raw = "overseer-dev-master-key-change-me"
		}
		sum := sha256.Sum256([]byte(raw + "|jwt"))
		jwtKey = sum[:]
	}
	return &Service{jwtKey: jwtKey}
}

func HashPassword(password string) (string, error) {
	password = strings.TrimSpace(password)
	if len(password) < 6 {
		return "", ErrWeakPassword
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func CheckPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Service) IssueToken(username string) (token string, expiresAt time.Time, err error) {
	username = strings.TrimSpace(username)
	if username == "" {
		username = DefaultUsername
	}
	expiresAt = time.Now().UTC().Add(TokenTTL)
	claims := Claims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   username,
			Issuer:    Issuer,
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token, err = t.SignedString(s.jwtKey)
	return token, expiresAt, err
}

func (s *Service) ParseToken(raw string) (*Claims, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "bearer ") {
		raw = strings.TrimSpace(raw[7:])
	}
	if raw == "" {
		return nil, fmt.Errorf("缺少 token")
	}
	parsed, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.jwtKey, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("无效 token")
	}
	return claims, nil
}
