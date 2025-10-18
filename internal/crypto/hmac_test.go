package crypto

import (
	"testing"
	"time"

	"github.com/unleaktrade/waitlist/internal/data"
)

func TestNewJWTHS256(t *testing.T) {
	j := NewJWTHS256(secret)
	if secret != string(j.k) {
		t.Errorf("incorrect NewJWTHS256 secret, got %s, want %s", j.k, secret)
		t.FailNow()
	}
}

func TestCreateHMAC(t *testing.T) {
	tt := []struct {
		name  string
		time  time.Time
		token string
		user  *data.User
		jwt   Token
		err   error
	}{
		{
			"HS256",
			time.UnixMicro(timestamp),
			tokenHS256,
			u,
			NewJWTHS256(secret),
			nil,
		},
		{
			"HS512",
			time.UnixMicro(timestamp),
			tokenHS512,
			u,
			NewJWTHS512(secret),
			nil,
		},
		{
			"HS256 user address missing",
			time.UnixMicro(timestamp),
			"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJhZGRyZXNzIjoiIiwiZW1haWwiOiJqb2huLmRvZUBtYWlsc2VydmljZS5jb20iLCJzcG9uc29yIjoiQWtwMW95alkxVlpHcHNkOGd3SEJDa0FEZTU3N2VWaEE0ZjhkQVN4VlRtM3MiLCJpc3MiOiJ1bmxlYWsudHJhZGUiLCJleHAiOjE2NDg1NTIsIm5iZiI6MTY0Nzk1MiwiaWF0IjoxNjQ3OTUyfQ.f335Wjp-wJ5zUaj0lTsX2UzhXnKCHFnUhNU-ilAfGhA",
			&data.User{
				Email:   email,
				Sponsor: sponsor,
			},
			NewJWTHS256(secret),
			nil,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			ss, err := tc.jwt.Create(tc.user, tc.time)
			if err != tc.err {
				t.Errorf("incorrect error, got %v, want %v", err, tc.err)
				t.Errorf("error creating user %v : %v", tc.user, err)
				t.FailNow()
			}

			if ss != tc.token {
				t.Errorf("incorrect token, got %s, want %s", ss, tc.token)
				t.FailNow()
			}
		})
	}
}

func TestExtractHMAC(t *testing.T) {
	j := NewJWTHS256(secret)
	now := time.Now()
	token, err := j.Create(u, now)
	tt := []struct {
		name                    string
		jwt                     Token
		ss                      string
		address, email, sponsor string
		err                     error
	}{
		{
			"valid token",
			j,
			token,
			address, email, sponsor,
			err,
		},
		{
			"expired token",
			j,
			tokenHS256,
			"", "", "",
			ErrInvalidToken,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			user, err := tc.jwt.Extract(tc.ss)
			if err != tc.err {
				t.Errorf("incorrect error, got %v, want %v", err, tc.err)
				t.FailNow()
			}
			if user != nil {
				if user.Address != tc.address {
					t.Errorf("incorrect address, got %v, want %v", user.Address, tc.address)
					t.FailNow()
				}
				if user.Email != tc.email {
					t.Errorf("incorrect email, got %v, want %v", user.Email, tc.email)
					t.FailNow()
				}
				if user.Sponsor != tc.sponsor {
					t.Errorf("incorrect sponsor, got %v, want %v", user.Sponsor, tc.sponsor)
					t.FailNow()
				}

				if user.UUID == "" {
					t.Errorf("UUID is incorrect, cannot be empty string")
					t.FailNow()
				}
				if user.Timestamp == 0 {
					t.Errorf("Timestamp is incorrect, cannot be 0")
					t.FailNow()
				}
			}
		})
	}

	t.Run("token without address", func(t *testing.T) {
		token, _ = j.Create(&data.User{
			Email:   email,
			Sponsor: sponsor,
		}, now)
		_, err = j.Extract(token)
		if err != ErrInvalidToken {
			t.Errorf("incorrect error, got %v, want %v", err, ErrInvalidToken)
			t.Errorf("%s", token)
			t.FailNow()
		}
	})

	t.Run("token without sponsor", func(t *testing.T) {
		token, _ = j.Create(&data.User{
			Address: address,
			Email:   email,
		}, now)
		_, err = j.Extract(token)
		if err != ErrInvalidToken {
			t.Errorf("incorrect error, got %v, want %v", err, ErrInvalidToken)
			t.Errorf("%s", token)
			t.FailNow()
		}
	})

}
