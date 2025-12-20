package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unleaktrade/waitlist/internal/cache"
	"github.com/unleaktrade/waitlist/internal/crypto"
	"github.com/unleaktrade/waitlist/internal/crypto/cipher"
	"github.com/unleaktrade/waitlist/internal/data"
	"github.com/unleaktrade/waitlist/internal/limiter"
	"github.com/unleaktrade/waitlist/internal/mailer"
)

const (
	sponsor    = "9mf2bkJf5TebjCYQYq3WcK61ruHTs3bpeQwW2s6WWj3A"
	testApiKey = "test-api-key"
)

func addAPIKey(req *http.Request) {
	req.Header.Set("UNLK-API-KEY", testApiKey)
}

func TestRegister(t *testing.T) {
	var db data.DB = data.MockDB
	k, _ := cipher.GenerateKey(32)
	app := &App{
		db,
		crypto.NewJWTHS256(k),
		&mailer.MockSmtpMailer,
		sync.WaitGroup{},
		limiter.NewUnlimited(),
		"path1",
		"path2",
		cache.New(),
		testApiKey,
	}
	r := setupRouter(app)
	tt := []struct {
		name                    string
		address, email, sponsor string
		status                  int
		err                     string
	}{
		{"valid user",
			"5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqVF",
			"john.doe@mailservice.com", sponsor,
			http.StatusAccepted,
			"",
		},
		{"empty address",
			"",
			"john.doe@mailservice.com", sponsor,
			http.StatusBadRequest,
			`{"error":"Key: 'User.Address' Error:Field validation for 'Address' failed on the 'required' tag"}`,
		},
		{"bullshit address",
			"123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ",
			"john.doe@mailservice.com", sponsor,
			http.StatusBadRequest,
			`{"error":"Key: 'User.Address' Error:Field validation for 'Address' failed on the 'solana_addr' tag"}`,
		},
		{"too short address",
			"5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqV",
			"john.doe@mailservice.com", sponsor,
			http.StatusBadRequest,
			`{"error":"Key: 'User.Address' Error:Field validation for 'Address' failed on the 'solana_addr' tag"}`,
		},
		{"too long address",
			"5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqVFEEEEEE",
			"john.doe@mailservice.com", sponsor,
			http.StatusBadRequest,
			`{"error":"Key: 'User.Address' Error:Field validation for 'Address' failed on the 'solana_addr' tag"}`,
		},
		{"empty email",
			"5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqVF",
			"", sponsor,
			http.StatusBadRequest,
			`{"error":"Key: 'User.Email' Error:Field validation for 'Email' failed on the 'required' tag"}`,
		},
		{"malformated email unsupported special characters",
			"5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqVF",
			"john/doe@email_^me.fr", sponsor,
			http.StatusBadRequest,
			`{"error":"Key: 'User.Email' Error:Field validation for 'Email' failed on the 'email' tag"}`,
		},
		{"malformated email no @",
			"5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqVF",
			"john.doe.email.fr", sponsor,
			http.StatusBadRequest,
			`{"error":"Key: 'User.Email' Error:Field validation for 'Email' failed on the 'email' tag"}`,
		},
		{"malformated email no user",
			"5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqVF",
			"@ovh.com", sponsor,
			http.StatusBadRequest,
			`{"error":"Key: 'User.Email' Error:Field validation for 'Email' failed on the 'email' tag"}`,
		},
		{"malformated email no domain",
			"5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqVF",
			"john.doe@", sponsor,
			http.StatusBadRequest,
			`{"error":"Key: 'User.Email' Error:Field validation for 'Email' failed on the 'email' tag"}`,
		},
		{"no sponsor address",
			"5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqVF",
			"john.doe@mailservice.com", "",
			http.StatusBadRequest,
			`{"error":"Key: 'User.Sponsor' Error:Field validation for 'Sponsor' failed on the 'required' tag"}`,
		},
		{"unvalid sponsor address",
			"5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqVF",
			"john.doe@mailservice.com", "A0oL22pbncZFoaZNZaHJUTMexkxbjq1BmfCgJbjVmMge", // valid format but not a real Solana address (not on curve)
			http.StatusBadRequest,
			`{"error":"Key: 'User.Sponsor' Error:Field validation for 'Sponsor' failed on the 'solana_addr' tag"}`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			address := tc.address
			email := tc.email
			sponsor := tc.sponsor

			jsonUser, _ := json.Marshal(data.User{
				Address: address,
				Email:   email,
				Sponsor: sponsor,
			})

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/register", bytes.NewBuffer(jsonUser))
			addAPIKey(req)
			r.ServeHTTP(w, req)

			switch tc.status {
			case http.StatusBadRequest:
				if w.Code != http.StatusBadRequest {
					t.Errorf("Status code is incorrect, got %d, want %d", w.Code, http.StatusBadRequest)
					t.FailNow()
				}

				if w.Body == nil {
					t.Errorf("Response body cannot be nil")
					t.FailNow()
				}

				if w.Body.String() != tc.err {
					t.Errorf("Error is incorrect, got %s, want %s", w.Body.String(), tc.err)
					t.FailNow()
				}
			case http.StatusAccepted:
				if w.Code != http.StatusAccepted {
					t.Errorf("Status code is incorrect, got %d, want %d", w.Code, http.StatusAccepted)
					t.FailNow()
				}

				var res struct {
					Hash string
				}
				err := json.NewDecoder(w.Body).Decode(&res)
				if err != nil {
					t.Errorf("Cannot decode response body %v, %v", w.Body, err)
					t.FailNow()
				}
				if res.Hash == "" {
					t.Errorf("incorrect hash, cannot be empty string")
				}

			default:
				t.Errorf("status %d not supported", tc.status)
				t.FailNow()
			}
		})
	}

}

func TestActivate(t *testing.T) {
	var db data.DB = data.NewMockDBContent([]string{sponsor})
	k, _ := cipher.GenerateKey(32)
	app := &App{
		db,
		crypto.NewJWTHS256(k),
		&mailer.MockSmtpMailer,
		sync.WaitGroup{},
		limiter.NewUnlimited(),
		"path1",
		"path2",
		cache.New(),
		testApiKey,
	}
	r := setupRouter(app)

	address, email := "5tsrsspeS4ARKhPzLpzqaMjwu2KzhvktoJFW1Lv7pqVF", "john.doe@mailservice.com"
	vt, _ := app.jwt.Create(&data.User{
		Address: address,
		Email:   email,
		Sponsor: sponsor}, time.Now())
	vh := app.jwt.Hash(vt)

	tt := []struct {
		name        string
		token, hash string
		status      int
		err         string
	}{
		{
			"valid token+hash",
			vt, vh,
			http.StatusCreated,
			"",
		},
		{
			"valid token invalid hash",
			vt, "hA5h",
			http.StatusUnauthorized,
			`{"error":"Unauthorized"}`,
		},
		{
			"no token no hash",
			"", "",
			http.StatusTemporaryRedirect,
			"",
		},
		{
			"malformated token",
			"eyJhbGciOiJIUzI1N.ZZZZZ.dczrracv.de", "hA5h",
			http.StatusUnauthorized,
			`{"error":"Unauthorized"}`,
		},
		{
			"malformated token no hash",
			"eyJhbGciOiJIUzI1N.ZZZZZ.dczrracv.de", "",
			http.StatusTemporaryRedirect,
			"",
		},
		{
			"malformated token fake hash",
			"eyJhbGciOiJIUzI1N.ZZZZZ.dczrracv.de", "hA5h",
			http.StatusUnauthorized,
			`{"error":"Unauthorized"}`,
		},
		{
			"fake token valid hash",
			"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJhZGRyZXNzIjoiIiwiZW1haWwiOiJqb2huLmRvZUBtYWlsc2VydmljZS5jb20iLCJ0eXBlIjoidGFsZW50IiwiaXNzIjoiZmFpcmhpdmUuaW8iLCJleHAiOjE2NDg1NTIsIm5iZiI6MTY0Nzk1MiwiaWF0IjoxNjQ3OTUyfQ.fU_Vi8s1vxU59oRSAnTGj3bN4veMeNuFYBLNWKuOnJE",
			"69E045328DCE6EAD2F52068EF4FB2232EBF12E82FF4712947B2DBC3CCEA8035000CF037B22EE1DD6B0C938AA9B7D329CC6DD111824F01D494FDD75B1150BD628",
			http.StatusUnauthorized,
			`{"error":"Unauthorized"}`,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			token := tc.token
			hash := tc.hash
			req, _ := http.NewRequest("POST", fmt.Sprintf("/activate/%s/%s", token, hash), nil)
			addAPIKey(req)
			r.ServeHTTP(w, req)

			switch tc.status {
			case http.StatusCreated:
				if w.Code != http.StatusCreated {
					t.Errorf("Status code is incorrect, got %d, want %d", w.Code, http.StatusCreated)
					t.Errorf("%s", w.Body.String())
					t.FailNow()
				}

				// var u data.User

				// json.NewDecoder(w.Body).Decode(&u)

				// if !u.IsValid() {
				// 	t.Errorf("user %v should be valid", u)
				// }

			case http.StatusNotFound:
				if w.Code != http.StatusNotFound {
					t.Errorf("Status code is incorrect, got %d, want %d", w.Code, http.StatusNotFound)
					t.Errorf("%s", w.Body.String())
					t.FailNow()
				}
			case http.StatusTemporaryRedirect:
				if w.Code != http.StatusTemporaryRedirect {
					t.Errorf("Status code is incorrect, got %d, want %d", w.Code, http.StatusTemporaryRedirect)
					t.Errorf("%s", w.Body.String())
					t.FailNow()
				}
			case http.StatusUnauthorized:
				if w.Code != http.StatusUnauthorized {
					t.Errorf("Status code is incorrect, got %d, want %d", w.Code, http.StatusUnauthorized)
					t.Errorf("%s", w.Body.String())
					t.FailNow()
				}

				if w.Body == nil {
					t.Errorf("Response body cannot be nil")
					t.FailNow()
				}

				if w.Body.String() != tc.err {
					t.Errorf("Error is incorrect, got %s, want %s", w.Body.String(), tc.err)
					t.FailNow()
				}
			default:
				t.Errorf("status %d not supported", tc.status)
				t.FailNow()
			}
		})
	}

	tt2 := []struct {
		name string
		db   data.DB
		code int
	}{
		{"faulty DB", data.NewMockErrDB([]string{sponsor}), http.StatusInternalServerError},
		{"fail finding address", data.NewMockErrFindingAddress([]string{sponsor}, address), http.StatusInternalServerError},
		{"fail finding sponsor", data.NewMockErrFindingAddress([]string{sponsor}, sponsor), http.StatusInternalServerError},
		{"address_nok_sponsor_nok", data.NewMockDBContent([]string{}), http.StatusBadRequest},
		{"address_nok_sponsor_ok", data.NewMockDBContent([]string{sponsor}), http.StatusCreated},
		{"address_ok_sponsor_nok", data.NewMockDBContent([]string{address}), http.StatusConflict},
		{"address_ok_sponsor_ok", data.NewMockDBContent([]string{address, sponsor}), http.StatusConflict},
	}
	for _, tc := range tt2 {
		t.Run(tc.name, func(t *testing.T) {
			app.db = tc.db
			r := setupRouter(app)
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", fmt.Sprintf("/activate/%s/%s", vt, vh), nil)
			addAPIKey(req)
			r.ServeHTTP(w, req)
			if w.Code != tc.code {
				t.Errorf("Status code is incorrect, got %d, want %d", w.Code, tc.code)
				t.FailNow()
			}
		})
	}
}

func TestHealth(t *testing.T) {
	var db data.DB = data.MockDB
	k, _ := cipher.GenerateKey(32)
	app := &App{
		db,
		crypto.NewJWTHS256(k),
		&mailer.MockSmtpMailer,
		sync.WaitGroup{},
		limiter.NewUnlimited(),
		"path1",
		"path2",
		cache.New(),
		testApiKey,
	}
	r := setupRouter(app)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	addAPIKey(req)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("incorrect status code, got %d, want %d\n", w.Code, http.StatusOK)
		t.FailNow()
	}

	headers := w.Result().Header
	if methods := headers.Get("Access-Control-Allow-Methods"); methods == "" || methods != "GET, POST, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods must be %q, got %q\n", "GET, POST, OPTIONS", methods)
		t.FailNow()
	}
	if headers.Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("incorrect Content-Type, got %q, want %q\n", headers.Get("Content-Type"), "application/json; charset=utf-8")
		t.FailNow()
	}

	if w.Body.Len() == 0 {
		t.Error("Body cannot be empty")
		t.FailNow()
	}

	want := `{"status":"ok"}`
	if strings.TrimSpace(w.Body.String()) != want {
		t.Errorf("incorrect body: got %q, want %q", w.Body.String(), want)
		t.FailNow()
	}
}

func TestRequireAPIKey(t *testing.T) {
	var db data.DB = data.MockDB
	k, _ := cipher.GenerateKey(32)
	app := &App{
		db,
		crypto.NewJWTHS256(k),
		&mailer.MockSmtpMailer,
		sync.WaitGroup{},
		limiter.NewUnlimited(),
		"path1",
		"path2",
		cache.New(),
		testApiKey,
	}
	r := setupRouter(app)

	tt := []struct {
		name   string
		key    string
		status int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "wrong-key", http.StatusUnauthorized},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/health", nil)
			if tc.key != "" {
				req.Header.Set("UNLK-API-KEY", tc.key)
			}
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Errorf("incorrect status, got %d, want %d", w.Code, tc.status)
				t.FailNow()
			}
		})
	}
}

func TestCheckWallet(t *testing.T) {
	var db data.DB = data.MockDB
	k, _ := cipher.GenerateKey(32)
	app := &App{
		db,
		crypto.NewJWTHS256(k),
		&mailer.MockSmtpMailer,
		sync.WaitGroup{},
		limiter.NewUnlimited(),
		"path1",
		"path2",
		cache.New(),
		testApiKey,
	}
	r := setupRouter(app)

	presentAddress := "Ggw2mrnWemEpJLYmGxBbMarEYsYTeQkToZcLVWsvk5Qg"
	missingAddress := "44DFptZSiuQSJBF9LemyMcqz6jei5EiVzkX7cdGxFW15"
	app.c.Add(presentAddress, time.Now().UnixMilli())

	tt := []struct {
		name    string
		address string
		status  int
		want    bool
	}{
		{"present", presentAddress, http.StatusOK, true},
		{"missing", missingAddress, http.StatusNotFound, false},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", fmt.Sprintf("/check-wallet/%s", tc.address), nil)
			addAPIKey(req)
			r.ServeHTTP(w, req)

			if w.Code != tc.status {
				t.Errorf("incorrect status, got %d, want %d", w.Code, tc.status)
				t.FailNow()
			}

			var res struct {
				Registered bool `json:"registered"`
			}
			if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
				t.Errorf("Cannot decode response body %v, %v", w.Body, err)
				t.FailNow()
			}
			if res.Registered != tc.want {
				t.Errorf("registered is incorrect, got %v, want %v", res.Registered, tc.want)
				t.FailNow()
			}
		})
	}
}

func TestList(t *testing.T) {
	var db data.DB = data.MockDB
	k, _ := cipher.GenerateKey(32)
	app := &App{
		db,
		crypto.NewJWTHS256(k),
		&mailer.MockSmtpMailer,
		sync.WaitGroup{},
		limiter.NewUnlimited(),
		"path1",
		"path2",
		cache.New(),
		testApiKey,
	}
	r := setupRouter(app)

	t.Run("json normal", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/%s/%s/list", app.secpath1, app.secpath2), nil)
		addAPIKey(req)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("incorrect status, got %d, want %d", w.Code, http.StatusOK)
			t.FailNow()
		}
		if w.Body == nil {
			t.Errorf("Response body cannot be nil")
			t.FailNow()
		}

		var res struct {
			Users []*data.User
			Count int
		}
		err := json.NewDecoder(w.Body).Decode(&res)
		if err != nil {
			t.Errorf("Cannot decode response body %v, %v", w.Body, err)
			t.FailNow()
		}

		count := data.UsersCountMock
		if res.Count != count {
			t.Errorf("incorrect count, got %d, want %d", res.Count, count)
			t.FailNow()
		}

		if len(res.Users) != res.Count {
			t.Errorf("incorrect length of Users array, got %d, want %d", len(res.Users), res.Count)
			t.FailNow()
		}
	})

	t.Run("csv", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/%s/%s/list?mime=csv", app.secpath1, app.secpath2), nil)
		addAPIKey(req)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("incorrect status, got %d, want %d", w.Code, http.StatusOK)
			t.FailNow()
		}
		if w.Body == nil {
			t.Errorf("Response body cannot be nil")
			t.FailNow()
		}
		if w.Body.Len() == 0 {
			t.Error("Body cannot be empty")
			t.FailNow()
		}

		headers := w.Result().Header
		if headers.Get("Content-Description") != "File Transfer" {
			t.Errorf("incorrect Content-Description header, got %s, want %s", headers.Get("Content-Description"), "File Transfer")
			t.FailNow()
		}
		if headers.Get("Content-Type") != "text/csv" {
			t.Errorf("incorrect Content-Type, got %q, want %q\n", headers.Get("Content-Type"), "text/csv")
			t.FailNow()
		}
		if !strings.Contains(headers.Get("Content-Disposition"), "attachment; filename=users_list_") {
			t.Errorf("incorrect Content-Disposition, must contain %q", "attachment; filename=users_list_")
			t.FailNow()
		}

	})

	tt := []struct {
		name         string
		path1, path2 string
		status       int
		body         string
	}{
		{"fakepath1", "fakepath1", app.secpath2, http.StatusNotFound, ""},
		{"fakepath2", app.secpath1, "fakepath2", http.StatusNotFound, ""},
		{"fakepaths", "fakepath1", "fakepath2", http.StatusNotFound, ""},
		{"missing path1", "", "fakepath2", http.StatusNotFound, "404 page not found"},
		{"missing path2", "fakepath1", "", http.StatusNotFound, ""},
		{"no path", "", "", http.StatusNotFound, ""},
	}
	for _, tc := range tt {
		t.Run("json_"+tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", fmt.Sprintf("/%s/%s/list", tc.path1, tc.path2), nil)
			addAPIKey(req)
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Errorf("incorrect status, got %d, want %d", w.Code, tc.status)
				t.FailNow()
			}
			if w.Body.String() != tc.body {
				t.Errorf("incorrect body, got %q, want %q", w.Body.String(), tc.body)
				t.FailNow()
			}
		})
	}

	tt2 := []struct {
		name        string
		offset, max string
		status      int
		ln          int
	}{
		{"empty strings", "", "", http.StatusOK, data.UsersCountMock},
		{"offset=foo", "foo", "", http.StatusBadRequest, 0},
		{"offset=0 max=foo", "0", "foo", http.StatusBadRequest, 0},
		{"max=foo", "", "foo", http.StatusBadRequest, 0},
		{fmt.Sprintf("offset=0 max=%d", data.UsersCountMock), "0", fmt.Sprintf("%d", data.UsersCountMock), http.StatusOK, data.UsersCountMock},
		{"offset=5", "5", "", http.StatusOK, data.UsersCountMock - 5},
		{"offset=5 max=3", "5", "3", http.StatusOK, 3},
		{"offset=5 max=0", "5", "0", http.StatusOK, 0},
		{"max=2", "", "2", http.StatusBadRequest, 0},
		{"offset=-2 max=5", fmt.Sprintf("%d", -2), "5", http.StatusInternalServerError, 0},
		{fmt.Sprintf("offset=%d max=5", data.UsersCountMock+1), fmt.Sprintf("%d", data.UsersCountMock+1), "5", http.StatusInternalServerError, 0},
		{"offset=5 max=-2", "5", fmt.Sprintf("%d", -2), http.StatusInternalServerError, 0},
		{fmt.Sprintf("offset=5 max=%d", data.UsersCountMock+1), "5", fmt.Sprintf("%d", data.UsersCountMock+1), http.StatusOK, data.UsersCountMock - 5},
		{fmt.Sprintf("offset=%d max=5", data.UsersCountMock), fmt.Sprintf("%d", data.UsersCountMock), "5", http.StatusInternalServerError, 0},
	}
	for _, tc := range tt2 {
		t.Run("json_"+tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", fmt.Sprintf("/%s/%s/list?offset=%s&max=%s", app.secpath1, app.secpath2, tc.offset, tc.max), nil)
			addAPIKey(req)
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Errorf("incorrect status, got %d, want %d", w.Code, tc.status)
				t.FailNow()
			}

			if w.Code == http.StatusOK {
				var res struct {
					Users []*data.User
					Count int
				}
				err := json.NewDecoder(w.Body).Decode(&res)
				if err != nil {
					t.Errorf("Cannot decode response body %v, %v", w.Body, err)
					t.FailNow()
				}

				count := tc.ln
				if res.Count != count {
					t.Errorf("incorrect count, got %d, want %d", res.Count, count)
					t.FailNow()
				}
			}
		})
	}

	app.db = data.NewMockErrDB([]string{sponsor})
	r = setupRouter(app)
	t.Run("json faulty DB", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/%s/%s/list", app.secpath1, app.secpath2), nil)
		addAPIKey(req)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("Status code is incorrect, got %d, want %d", w.Code, http.StatusInternalServerError)
			t.FailNow()
		}
	})
}
