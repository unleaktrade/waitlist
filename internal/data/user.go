package data

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type User struct {
	Address   string `json:"address" binding:"required,solana_addr" validate:"required,solana_addr"`
	Email     string `json:"email" binding:"required,email" validate:"required,email"`
	UUID      string `json:"uuid,omitempty" validate:"required,uuid"`
	Timestamp int64  `json:"timestamp,omitempty" validate:"gt=0"`
	Sponsor   string `json:"sponsor" binding:"required,solana_addr" validate:"required,solana_addr"`
}

var validate = validator.New()

func init() {
	validate.RegisterValidation("solana_addr", validateSolanaAddress)

	// Register with Gin's validator
	if v, ok := binding.Validator.Engine().(*validator.Validate); ok {
		v.RegisterValidation("solana_addr", validateSolanaAddress)
	}
}

func validateSolanaAddress(fl validator.FieldLevel) bool {
	address := fl.Field().String()
	pubkey, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		return false
	}
	return solana.IsOnCurve(pubkey[:])
}

func (u *User) Setup() {
	u.UUID = uuid.New().String()
	u.Timestamp = time.Now().UnixMilli()
}

func NewUser(a, e, s string) *User {
	u := &User{
		Address: a,
		Email:   e,
		Sponsor: s,
	}
	u.Setup()
	return u
}

// IsValid tests if all fields are valid
func (u *User) IsValid() bool {
	return nil == validate.Struct(u)
}

// IsSet tests if only required fields are valid
func (u *User) IsSet() bool {
	err := validate.StructExcept(u, "UUID", "Timestamp")
	if err != nil {
		log.Print(err)
	}
	return nil == err
}

func (u User) String() string {
	r, _ := json.Marshal(&struct {
		*User
		Timestamp string `json:"timestamp"`
	}{
		User:      &u,
		Timestamp: time.UnixMilli(u.Timestamp).Format(time.RFC3339Nano),
	})
	return string(r)
}
