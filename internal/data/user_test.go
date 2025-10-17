package data

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

const sponsor = "BDHCyVLMrJbPriFaopTzNFeHBqhtCQUUgnC3aBK5gNrq"

func TestSetup(t *testing.T) {

	u := User{}
	u.Setup()

	if u.UUID == "" {
		t.Errorf("UUID is incorrect, cannot be empty")
		t.FailNow()
	}

	if _, err := uuid.Parse(u.UUID); err != nil {
		t.Errorf("UUID is incorrect, cannot be parsed: %v", err)
		t.FailNow()
	}

	if u.Timestamp == 0 {
		t.Errorf("Timestamp is incorrect, cannot be set")
		t.FailNow()
	}
}

// Test NewUser() + defacto IsValid() & IsSet()
func TestNewUser(t *testing.T) {
	type errorDetails struct {
		field string
		tag   string
		value interface{}
	}

	validAddress := "8mxgS3kGYjmCwyktyBqcAxxYy4G32vUKuCNEUdpAySPk"
	validUser := NewUser(validAddress, "john.doe@mailservice.com", sponsor)

	noUUIDUser := *validUser
	noUUIDUser.UUID = ""

	invalidUUIDUser := *validUser
	invalidUUIDUser.UUID = "fakeUUID"

	invalidTimestampUser := *validUser
	invalidTimestampUser.Timestamp = 0

	tt := []struct {
		name       string
		u          *User
		err        *errorDetails
		valid, set bool
	}{
		{"valid_user", validUser, nil, true, true},
		{"invalid_user_address",
			NewUser("9nagS3kGYjmCwyktyBqcAxxYy4G32vUKuCNEUdpAySPk", "john.doe@mailservice.com", sponsor),
			&errorDetails{"Address", "solana_addr", "9nagS3kGYjmCwyktyBqcAxxYy4G32vUKuCNEUdpAySPk"},
			false, false,
		},
		{"missing_user_address",
			NewUser("", "john.doe@mailservice.com", sponsor),
			&errorDetails{"Address", "required", ""},
			false, false,
		},
		{"invalid_email",
			NewUser(validAddress, "john.doemailservice.com", sponsor),
			&errorDetails{"Email", "email", "john.doemailservice.com"},
			false, false,
		},
		{"missing_email",
			NewUser(validAddress, "", sponsor),
			&errorDetails{"Email", "required", ""},
			false, false,
		},
		{"invalid_sponsor",
			NewUser(validAddress, "john.doemail@service.com", "9nagS3kGYjmCwyktyBqcAxxYy4G32vUKuCNEUdpAySPk"),
			&errorDetails{"Sponsor", "solana_addr", "9nagS3kGYjmCwyktyBqcAxxYy4G32vUKuCNEUdpAySPk"},
			false, false,
		},
		{"missing_sponsor",
			NewUser(validAddress, "john.doemail@service.com", ""),
			&errorDetails{"Sponsor", "required", ""},
			false, false,
		},
		{"missing_uuid",
			&noUUIDUser,
			&errorDetails{"UUID", "required", ""},
			false, true,
		},
		{"invalid_uuid",
			&invalidUUIDUser,
			&errorDetails{"UUID", "uuid", "fakeUUID"},
			false, true,
		},
		{"invalid_timestamp",
			&invalidTimestampUser,
			&errorDetails{"Timestamp", "gt", int64(0)},
			false, true,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			u := tc.u

			err := validate.Struct(u)
			if err != nil {
				if tc.err != nil {
					for _, e := range err.(validator.ValidationErrors) {
						if e.Field() != tc.err.field {
							t.Errorf("Field is incorrect, got %v, want %v", e.Field(), tc.err.field)
						}
						if e.Tag() != tc.err.tag {
							t.Errorf("Tag is incorrect, got %v, want %v", e.Tag(), tc.err.tag)
						}
						if e.Value() != tc.err.value {
							t.Errorf("Value is incorrect, got %v, want %v", e.Value(), tc.err.value)
						}
					}
				} else {
					t.Errorf("user %v is not valid, %v ", *u, err)
					t.FailNow()
				}
			}

			if u.IsValid() != tc.valid {
				t.Errorf("incorrect complete validation, got %v, want %v", u.IsValid(), tc.valid)
			}

			if u.IsSet() != tc.set {
				t.Errorf("incorrect partial validation, got %v, want %v", u.IsSet(), tc.set)
			}
		})
	}

}

func TestMarshalling(t *testing.T) {
	address := "HFcC6HuJzd7uGLMJ9YqTmLYLXbBwYn43JFCwExHEBw8r"
	email := "john.doe@mailservice.com"
	u := NewUser(address, email, sponsor)
	jsonUser, _ := json.Marshal(u)
	n := 5
	m := make(map[string]interface{}, n)
	if err := json.Unmarshal(jsonUser, &m); err != nil {
		t.Errorf("Cannot unmarshal marshaled user: %v", err)
		t.FailNow()
	}
	if len(m) != n {
		t.Errorf("Incorrect number of field in json, got %d, want %d", len(m), n)
		t.FailNow()
	}

	u = &User{
		Address: address,
		Email:   email,
		Sponsor: sponsor,
	}
	jsonUser, _ = json.Marshal(u)
	n = 3
	m = make(map[string]interface{}, n)
	if err := json.Unmarshal(jsonUser, &m); err != nil {
		t.Errorf("Cannot unmarshal marshaled user: %v", err)
		t.FailNow()
	}
	if len(m) != n {
		t.Errorf("Incorrect number of field in json, got %d, want %d", len(m), n)
		t.FailNow()
	}
}

func TestString(t *testing.T) {
	a1, a2 := "0CWE15QhD8pQYhHshhKphoLAYNZxr5phFLNJnrmC6oFTy", "FZR973wQgXGTDg3TXDTAuuE1jNeSWgHCBZFYmF34gBTJ"
	e1, e2 := "user1@domain.com", "user2@domain.com"
	id1, id2 := "4a8e9808-563e-4761-a8fa-305fef099a3e", "942a5811-926d-4014-baff-ef707f38407e"
	tm1, tm2 := 1683907220519, 1683807190432
	s1, s2 := "B7oeZae4KhWnbrsBYczPvU2iWhVungSdEzTBKD6pfpHo", "B4RRVRTrPoE5PmPkoRG7L3Ae7EmWkqbC6D9Zf3fx4mGH"

	tt := []struct {
		name string
		u    *User
		exp  string
	}{
		{
			"valid_user1",
			&User{a1, e1, id1, int64(tm1), s1},
			"{\"address\":\"0CWE15QhD8pQYhHshhKphoLAYNZxr5phFLNJnrmC6oFTy\",\"email\":\"user1@domain.com\",\"uuid\":\"4a8e9808-563e-4761-a8fa-305fef099a3e\",\"sponsor\":\"B7oeZae4KhWnbrsBYczPvU2iWhVungSdEzTBKD6pfpHo\",\"timestamp\":\"2023-05-12T18:00:20.519+02:00\"}",
		},
		{
			"valid_user2",
			&User{a2, e2, id2, int64(tm2), s2},
			"{\"address\":\"FZR973wQgXGTDg3TXDTAuuE1jNeSWgHCBZFYmF34gBTJ\",\"email\":\"user2@domain.com\",\"uuid\":\"942a5811-926d-4014-baff-ef707f38407e\",\"sponsor\":\"B4RRVRTrPoE5PmPkoRG7L3Ae7EmWkqbC6D9Zf3fx4mGH\",\"timestamp\":\"2023-05-11T14:13:10.432+02:00\"}",
		},
		{
			"empty_address",
			&User{"", e2, id2, int64(tm2), s2},
			"{\"address\":\"\",\"email\":\"user2@domain.com\",\"uuid\":\"942a5811-926d-4014-baff-ef707f38407e\",\"sponsor\":\"B4RRVRTrPoE5PmPkoRG7L3Ae7EmWkqbC6D9Zf3fx4mGH\",\"timestamp\":\"2023-05-11T14:13:10.432+02:00\"}",
		},
		{
			"empty_address_empty_sponsor",
			&User{"", e2, id2, int64(tm2), ""},
			"{\"address\":\"\",\"email\":\"user2@domain.com\",\"uuid\":\"942a5811-926d-4014-baff-ef707f38407e\",\"sponsor\":\"\",\"timestamp\":\"2023-05-11T14:13:10.432+02:00\"}",
		},
		{
			"no_email",
			&User{a2, "", id2, int64(tm2), s2},
			"{\"address\":\"FZR973wQgXGTDg3TXDTAuuE1jNeSWgHCBZFYmF34gBTJ\",\"uuid\":\"942a5811-926d-4014-baff-ef707f38407e\",\"sponsor\":\"B4RRVRTrPoE5PmPkoRG7L3Ae7EmWkqbC6D9Zf3fx4mGH\",\"timestamp\":\"2023-05-11T14:13:10.432+02:00\"}",
		},
		{
			"no_uuid",
			&User{a2, e2, "", int64(tm2), s2},
			"{\"address\":\"FZR973wQgXGTDg3TXDTAuuE1jNeSWgHCBZFYmF34gBTJ\",\"email\":\"user2@domain.com\",\"sponsor\":\"B4RRVRTrPoE5PmPkoRG7L3Ae7EmWkqbC6D9Zf3fx4mGH\",\"timestamp\":\"2023-05-11T14:13:10.432+02:00\"}",
		},
		{
			"no_uuid_no_type",
			&User{a2, e2, "", int64(tm2), s2},
			"{\"address\":\"FZR973wQgXGTDg3TXDTAuuE1jNeSWgHCBZFYmF34gBTJ\",\"email\":\"user2@domain.com\",\"sponsor\":\"B4RRVRTrPoE5PmPkoRG7L3Ae7EmWkqbC6D9Zf3fx4mGH\",\"timestamp\":\"2023-05-11T14:13:10.432+02:00\"}",
		},
		{
			"epoch_T0_no_timestamp",
			&User{a1, e1, id1, 0, s1},
			"{\"address\":\"0CWE15QhD8pQYhHshhKphoLAYNZxr5phFLNJnrmC6oFTy\",\"email\":\"user1@domain.com\",\"uuid\":\"4a8e9808-563e-4761-a8fa-305fef099a3e\",\"sponsor\":\"B7oeZae4KhWnbrsBYczPvU2iWhVungSdEzTBKD6pfpHo\"}",
		},
		{
			"epoch_T0",
			&User{a1, e1, id1, 0, s1},
			"{\"address\":\"0CWE15QhD8pQYhHshhKphoLAYNZxr5phFLNJnrmC6oFTy\",\"email\":\"user1@domain.com\",\"uuid\":\"4a8e9808-563e-4761-a8fa-305fef099a3e\",\"sponsor\":\"B7oeZae4KhWnbrsBYczPvU2iWhVungSdEzTBKD6pfpHo\",\"timestamp\":\"1970-01-01T00:00:00.000+00:00\"}",
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			var un struct { // same struct than User String()
				*User
				Timestamp string `json:"timestamp"`
			}
			err := json.Unmarshal([]byte(tc.exp), &un)
			if err != nil {
				t.Errorf("cannot unmarshal %v, got error %v", tc.exp, err)
				t.FailNow()
			}
			d, err := time.Parse(time.RFC3339Nano, un.Timestamp)
			if err != nil && un.Timestamp != "" {
				t.Errorf("cannot parse time %s: %v", tc.exp, err)
				t.FailNow()
			}

			u := &User{
				Address: un.Address,
				Email:   un.Email,
				UUID:    un.UUID,
				Sponsor: un.Sponsor,
			}

			if un.Timestamp != "" {
				u.Timestamp = d.UnixMilli()
			}

			if u.Address != tc.u.Address {
				t.Errorf("Address is incorrect, got %s, want %s", u.Address, tc.u.Address)
				t.FailNow()
			}
			if u.Email != tc.u.Email {
				t.Errorf("Email is incorrect, got %s, want %s", u.Email, tc.u.Email)
				t.FailNow()
			}
			if u.UUID != tc.u.UUID {
				t.Errorf("UUID is incorrect, got %s, want %s", u.UUID, tc.u.UUID)
				t.FailNow()
			}
			if u.Timestamp != tc.u.Timestamp {
				t.Errorf("Timestamp is incorrect, got %d, want %d", u.Timestamp, tc.u.Timestamp)
				t.FailNow()
			}
			if u.Sponsor != tc.u.Sponsor {
				t.Errorf("Sponsor is incorrect, got %s, want %s", u.Sponsor, tc.u.Sponsor)
				t.FailNow()
			}
		})
	}
}
