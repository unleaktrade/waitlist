package crypto

import (
	"github.com/golang-jwt/jwt/v4"
	"github.com/unleaktrade/waitlist/internal/data"
)

type JWTHMAC struct {
	JWTBase[[]byte]
}

func NewJWTHS256(s string) *JWTHMAC {
	return &JWTHMAC{JWTBase[[]byte]{jwt.SigningMethodHS256, []byte(s)}}
}

func NewJWTHS512(s string) *JWTHMAC {
	return &JWTHMAC{JWTBase[[]byte]{jwt.SigningMethodHS512, []byte(s)}}
}

func (j JWTHMAC) Extract(token string) (u *data.User, err error) {
	return extract[*jwt.SigningMethodHMAC](token, j.k)
}
