package crypto

import (
	"testing"

	"github.com/unleaktrade/waitlist/internal/data"
)

const (
	secret     = "VERY_SECURE_JWT_SECRET_L0L"
	address    = "3VfHtsBKQkKr7H2jB6CMvrnKekyJjpMZJA5kaPhQwjHh"
	sponsor    = "Akp1oyjY1VZGpsd8gwHBCkADe577eVhA4f8dASxVTm3s" // real address but empty balance
	email      = "john.doe@mailservice.com"
	timestamp  = 1647952128425
	tokenHS256 = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJhZGRyZXNzIjoiM1ZmSHRzQktRa0tyN0gyakI2Q012cm5LZWt5SmpwTVpKQTVrYVBoUXdqSGgiLCJlbWFpbCI6ImpvaG4uZG9lQG1haWxzZXJ2aWNlLmNvbSIsInNwb25zb3IiOiJBa3Axb3lqWTFWWkdwc2Q4Z3dIQkNrQURlNTc3ZVZoQTRmOGRBU3hWVG0zcyIsImlzcyI6InVubGVhay50cmFkZSIsImV4cCI6MTY0ODU1MiwibmJmIjoxNjQ3OTUyLCJpYXQiOjE2NDc5NTJ9.CBZniUCXzdWlgQaO5cwdNQguiNiEcLQYtJZm98b3X5Q"
	tokenHS512 = "eyJhbGciOiJIUzUxMiIsInR5cCI6IkpXVCJ9.eyJhZGRyZXNzIjoiM1ZmSHRzQktRa0tyN0gyakI2Q012cm5LZWt5SmpwTVpKQTVrYVBoUXdqSGgiLCJlbWFpbCI6ImpvaG4uZG9lQG1haWxzZXJ2aWNlLmNvbSIsInNwb25zb3IiOiJBa3Axb3lqWTFWWkdwc2Q4Z3dIQkNrQURlNTc3ZVZoQTRmOGRBU3hWVG0zcyIsImlzcyI6InVubGVhay50cmFkZSIsImV4cCI6MTY0ODU1MiwibmJmIjoxNjQ3OTUyLCJpYXQiOjE2NDc5NTJ9.VNOkxIGabIts99scWdsQeszMSq7YAQSkzz-T5l1ikg9WXIBuyO9ZWyCwoUmtAGIPl-B5MQSKzxNKascvbxH4ow"
)

var (
	u = &data.User{
		Address: address,
		Email:   email,
		Sponsor: sponsor,
	}
)

func TestHash(t *testing.T) {
	tt := []struct {
		name  string
		token string
		hash  string
	}{
		{
			"hash HS256 token",
			tokenHS256,
			"5719B140ABEFF9FD44DD610C4C0673A10FCEDCEB3A14F09FC69D90C93D63EAAF37A36E735290679C1E531FA3C5C08B8924FF738DA0D5F384493899CDDD1CF597",
		},
		{
			"hash HS512 token",
			tokenHS512,
			"AF0529BDE462FE6E0666BE6B26F6487F5E6C01A3C7907FCA82A7914879038506609035FE8589E651F2F9EC2B231F6A6344DB15514C248D0ABA6CC3E390CE98BB",
		},
		{
			"hash DATA",
			"DATA",
			"084E310EDCFBD2591B9997B55870D1AE49BCF1AEE7C74EFB4236CE8A9F28A6CE5FBF3394742969DFE578031822975EA44DE0C2AE68163368C8AA0185263FC874",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			h := hash(tc.token)
			if h != tc.hash {
				t.Errorf("incorrect hash, got %s, want %s", h, tc.hash)
				t.FailNow()
			}
		})
	}
}
