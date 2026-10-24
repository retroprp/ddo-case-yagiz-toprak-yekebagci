package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"bakimplan/internal/middleware"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const UserContextKey contextKey = "auth_user"

type Claims struct {
	UserID   int    `json:"user_id"`
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func jwtSecret() []byte {
	s := os.Getenv("JWT_SECRET")
	if s == "" {
		s = "ddo-super-secret-jwt-key-2026-bakimplan"
	}
	return []byte(s)
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func CheckPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func GenerateToken(userID int, email, fullName, role string) (string, error) {
	claims := &Claims{
		UserID:   userID,
		Email:    email,
		FullName: fullName,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtSecret())
}

func ParseToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (any, error) {
		return jwtSecret(), nil
	})
	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

// Authenticate: JWT Token kontrolü yapar (Yoksa veya geçersizse 401 döner)
func Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			middleware.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Kimlik doğrulama başlığı (Bearer token) eksik veya geçersiz.")
			return
		}

		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := ParseToken(tokenStr)
		if err != nil {
			middleware.WriteError(w, http.StatusUnauthorized, "INVALID_TOKEN", "Oturum süresi dolmuş veya geçersiz token.")
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRoles: Belirtilen roller dışında erişimi 403 Forbidden ile reddeder
func RequireRoles(allowedRoles ...string) func(http.Handler) http.Handler {
	allowedMap := make(map[string]bool, len(allowedRoles))
	for _, role := range allowedRoles {
		allowedMap[role] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := GetUserFromContext(r.Context())
			if !ok {
				middleware.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Kullanıcı oturumu bulunamadı.")
				return
			}

			if !allowedMap[claims.Role] {
				middleware.WriteError(w, http.StatusForbidden, "FORBIDDEN_ROLE", "Bu işlem için yetkiniz ("+claims.Role+") bulunmamaktadır.")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func GetUserFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(UserContextKey).(*Claims)
	return claims, ok
}
