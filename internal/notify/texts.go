package notify

import (
	"embed"
	"fmt"
	"math"
	"strings"
	"text/template"
	"time"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.tmpl"))

// Texts renders message texts. Templates live in templates/ and are embedded in the binary.
type Texts struct {
	AppName    string
	SMSAppHash string        // optional Android SMS Retriever hash
	CodeTTL    time.Duration // shown to the user
}

type textData struct {
	AppName string
	Code    string
	AppHash string
	TTL     string // "10 минут"
}

func (t Texts) data(code string) textData {
	return textData{AppName: t.AppName, Code: code, AppHash: t.SMSAppHash, TTL: minutes(t.CodeTTL)}
}

// minutes renders d in whole minutes, rounded up so the text never promises more time than
// there is, with the Russian plural form: 1 минуту, 2 минуты, 5 минут, 11 минут, 21 минуту.
func minutes(d time.Duration) string {
	n := int(math.Ceil(d.Minutes()))
	form := "минут"
	switch {
	case n%100 >= 11 && n%100 <= 14:
	case n%10 == 1:
		form = "минуту"
	case n%10 >= 2 && n%10 <= 4:
		form = "минуты"
	}
	return fmt.Sprintf("%d %s", n, form)
}

func (t Texts) PhoneCode(code string) (string, error) {
	return render("sms_phone_code", t.data(code))
}

func (t Texts) EmailVerification(code string) (subject, body string, err error) {
	return renderMail("email_verify", t.data(code))
}

func (t Texts) PasswordReset(code string) (subject, body string, err error) {
	return renderMail("password_reset", t.data(code))
}

func renderMail(name string, d textData) (string, string, error) {
	subject, err := render(name+"_subject", d)
	if err != nil {
		return "", "", err
	}
	body, err := render(name+"_body", d)
	return subject, body, err
}

func render(name string, d textData) (string, error) {
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, name, d); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}
