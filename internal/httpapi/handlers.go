package httpapi

import (
	"context"
	"time"

	"github.com/AntiqueViolet/go_kachalka_backend/internal/account"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/auth"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/httpapi/oapi"
	"github.com/AntiqueViolet/go_kachalka_backend/internal/store"
)

// handlers adapts the services to the generated strict server interface.
// Errors are returned as-is and rendered by errorHandlers.response.
type handlers struct {
	auth    *auth.Service
	account *account.Service
}

var _ oapi.StrictServerInterface = handlers{}

func seconds(d time.Duration) int { return int(d / time.Second) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func toUser(u store.User) oapi.User {
	return oapi.User{
		Id:            u.ID,
		Phone:         u.Phone,
		Email:         u.Email,
		EmailVerified: u.EmailVerifiedAt != nil,
		CreatedAt:     u.CreatedAt,
	}
}

func toTokens(t auth.Tokens) oapi.Tokens {
	return oapi.Tokens{
		TokenType:        oapi.Bearer,
		AccessToken:      t.Access,
		AccessExpiresIn:  seconds(t.AccessExpiresIn),
		RefreshToken:     t.Refresh,
		RefreshExpiresIn: seconds(t.RefreshExpiresIn),
	}
}

func toAuthResult(r auth.Result) oapi.AuthResult {
	return oapi.AuthResult{User: toUser(r.User), Tokens: toTokens(r.Tokens)}
}

func (h handlers) RegisterStart(ctx context.Context, req oapi.RegisterStartRequestObject) (oapi.RegisterStartResponseObject, error) {
	s, err := h.auth.StartRegistration(ctx, req.Body.Phone)
	if err != nil {
		return nil, err
	}
	return oapi.RegisterStart202JSONResponse{
		VerificationId: s.VerificationID,
		ExpiresIn:      seconds(s.ExpiresIn),
		ResendAfter:    seconds(s.ResendAfter),
	}, nil
}

func (h handlers) RegisterVerify(ctx context.Context, req oapi.RegisterVerifyRequestObject) (oapi.RegisterVerifyResponseObject, error) {
	rt, err := h.auth.VerifyPhone(ctx, req.Body.VerificationId, req.Body.Code)
	if err != nil {
		return nil, err
	}
	return oapi.RegisterVerify200JSONResponse{RegistrationToken: rt.Token, ExpiresIn: seconds(rt.ExpiresIn)}, nil
}

func (h handlers) Register(ctx context.Context, req oapi.RegisterRequestObject) (oapi.RegisterResponseObject, error) {
	res, err := h.auth.Register(ctx, auth.RegisterInput{
		RegistrationToken: req.Body.RegistrationToken,
		Password:          req.Body.Password,
		Email:             req.Body.Email,
		DeviceName:        deref(req.Body.DeviceName),
	})
	if err != nil {
		return nil, err
	}
	return oapi.Register201JSONResponse(toAuthResult(res)), nil
}

func (h handlers) Login(ctx context.Context, req oapi.LoginRequestObject) (oapi.LoginResponseObject, error) {
	res, err := h.auth.Login(ctx, auth.LoginInput{
		Phone:      req.Body.Phone,
		Password:   req.Body.Password,
		DeviceName: deref(req.Body.DeviceName),
	})
	if err != nil {
		return nil, err
	}
	return oapi.Login200JSONResponse(toAuthResult(res)), nil
}

func (h handlers) Refresh(ctx context.Context, req oapi.RefreshRequestObject) (oapi.RefreshResponseObject, error) {
	t, err := h.auth.Refresh(ctx, req.Body.RefreshToken)
	if err != nil {
		return nil, err
	}
	return oapi.Refresh200JSONResponse{Tokens: toTokens(t)}, nil
}

func (h handlers) Logout(ctx context.Context, req oapi.LogoutRequestObject) (oapi.LogoutResponseObject, error) {
	a, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	all := req.Body != nil && req.Body.AllDevices != nil && *req.Body.AllDevices
	if err := h.auth.Logout(ctx, a, all); err != nil {
		return nil, err
	}
	return oapi.Logout204Response{}, nil
}

func (h handlers) ForgotPassword(ctx context.Context, req oapi.ForgotPasswordRequestObject) (oapi.ForgotPasswordResponseObject, error) {
	after, err := h.auth.ForgotPassword(ctx, req.Body.Email)
	if err != nil {
		return nil, err
	}
	return oapi.ForgotPassword202JSONResponse{ResendAfter: seconds(after)}, nil
}

func (h handlers) ResetPassword(ctx context.Context, req oapi.ResetPasswordRequestObject) (oapi.ResetPasswordResponseObject, error) {
	err := h.auth.ResetPassword(ctx, auth.ResetInput{Email: req.Body.Email, Code: req.Body.Code, NewPassword: req.Body.NewPassword})
	if err != nil {
		return nil, err
	}
	return oapi.ResetPassword204Response{}, nil
}

func (h handlers) GetMe(ctx context.Context, _ oapi.GetMeRequestObject) (oapi.GetMeResponseObject, error) {
	a, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	u, err := h.account.Me(ctx, a)
	if err != nil {
		return nil, err
	}
	return oapi.GetMe200JSONResponse(toUser(u)), nil
}

func (h handlers) DeleteMe(ctx context.Context, req oapi.DeleteMeRequestObject) (oapi.DeleteMeResponseObject, error) {
	a, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.account.Delete(ctx, a, req.Body.Password); err != nil {
		return nil, err
	}
	return oapi.DeleteMe204Response{}, nil
}

func (h handlers) ChangeEmail(ctx context.Context, req oapi.ChangeEmailRequestObject) (oapi.ChangeEmailResponseObject, error) {
	a, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	u, sent, err := h.account.ChangeEmail(ctx, a, req.Body.Email, req.Body.Password)
	if err != nil {
		return nil, err
	}
	return oapi.ChangeEmail202JSONResponse{
		User:        toUser(u),
		ExpiresIn:   seconds(sent.ExpiresIn),
		ResendAfter: seconds(sent.ResendAfter),
	}, nil
}

func (h handlers) VerifyEmail(ctx context.Context, req oapi.VerifyEmailRequestObject) (oapi.VerifyEmailResponseObject, error) {
	a, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	u, err := h.account.VerifyEmail(ctx, a, req.Body.Code)
	if err != nil {
		return nil, err
	}
	return oapi.VerifyEmail200JSONResponse(toUser(u)), nil
}

func (h handlers) ResendEmailCode(ctx context.Context, _ oapi.ResendEmailCodeRequestObject) (oapi.ResendEmailCodeResponseObject, error) {
	a, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	sent, err := h.account.ResendEmailCode(ctx, a)
	if err != nil {
		return nil, err
	}
	return oapi.ResendEmailCode202JSONResponse{ExpiresIn: seconds(sent.ExpiresIn), ResendAfter: seconds(sent.ResendAfter)}, nil
}

func (h handlers) ChangePassword(ctx context.Context, req oapi.ChangePasswordRequestObject) (oapi.ChangePasswordResponseObject, error) {
	a, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.account.ChangePassword(ctx, a, req.Body.OldPassword, req.Body.NewPassword); err != nil {
		return nil, err
	}
	return oapi.ChangePassword204Response{}, nil
}
