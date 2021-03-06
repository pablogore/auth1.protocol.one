package manager

import (
	"fmt"
	"github.com/ProtocolONE/auth1.protocol.one/pkg/config"
	"github.com/ProtocolONE/auth1.protocol.one/pkg/database"
	"github.com/ProtocolONE/auth1.protocol.one/pkg/models"
	"github.com/ProtocolONE/auth1.protocol.one/pkg/service"
	"github.com/ProtocolONE/auth1.protocol.one/pkg/validator"
	"github.com/ProtocolONE/authone-jwt-verifier-golang"
	"github.com/globalsign/mgo/bson"
	"github.com/jinzhu/copier"
	"github.com/labstack/echo/v4"
	"github.com/ory/hydra/sdk/go/hydra/client/admin"
	models2 "github.com/ory/hydra/sdk/go/hydra/models"
	"github.com/pkg/errors"
	"gopkg.in/tomb.v2"
	"time"
)

var (
	loginRememberKey   = "login_remember"
	clientIdSessionKey = "oauth_client_id"
	logoutSessionKey   = "oauth_logout_redirect_uri"
	logoutHydraUrl     = "/oauth2/auth/sessions/login/revoke"
)

// OauthManagerInterface describes of methods for the manager.
type OauthManagerInterface interface {
	// CheckAuth is a cookie based authentication check.
	//
	//  If the user has previously been authorized and selected the option "remember me",
	//  then this method automatically authorizes the user.
	//
	//  If the user does not have an authorization session, his email address will be returned in order
	//  to offer him authorization under the previous account.
	//
	//  If no authorization was found, then a list of social networks is returned (if available) in order to prompt
	//  the user to log in through them, and not just by login and password.
	CheckAuth(echo.Context, *models.Oauth2LoginForm) (string, *models.User, []*models.AppIdentityProvider, string, *models.GeneralError)

	// Auth authorizes a user based on login and password, previous login or
	// one-time authorization token (obtained after authorization through social networks).
	//
	// After successful authorization, the URL for the redirect will be returned to pass the agreement consent process.
	Auth(echo.Context, *models.Oauth2LoginSubmitForm) (string, *models.GeneralError)

	// Consent prompts the user to accept the consent.
	Consent(echo.Context, *models.Oauth2ConsentForm) ([]string, *models.GeneralError)

	// Consent accepts the consent.
	ConsentSubmit(echo.Context, *models.Oauth2ConsentSubmitForm) (string, *models.GeneralError)

	// GetScopes returns a list of available scope for the application.
	GetScopes() ([]string, error)

	// Introspect checks the token and returns its contents.
	//
	// Contains an access token's session data as specified by IETF RFC 7662, see:
	// https://tools.ietf.org/html/rfc7662
	Introspect(echo.Context, *models.Oauth2IntrospectForm) (*models.Oauth2TokenIntrospection, *models.GeneralError)

	// SignUp registers a new user using login and password.
	//
	// After successful registration, the URL for the redirect will be returned to pass the agreement consent process.
	SignUp(echo.Context, *models.Oauth2SignUpForm) (string, *models.GeneralError)

	// CallBack verifies the result of oauth2 authorization.
	//
	// The method is implemented for applications that do not have their own backend,
	// for example, an application for a computer or a SPA.
	//
	// The page, using the JS SDK, will transmit through the PostMessage and the callback function the result of
	// the authorization, the token and the time of its completion.
	CallBack(echo.Context, *models.Oauth2CallBackForm) (*models.Oauth2CallBackResponse, *models.GeneralError)

	// Logout removes the authentication cookie and redirects the user to the specified URL.
	//
	// Url should be in the list of trusted ones, as well as during authorization and registration.
	Logout(echo.Context, *models.Oauth2LogoutForm) (string, *models.GeneralError)
}

// OauthManager is the oauth manager.
type OauthManager struct {
	hydraConfig             *config.Hydra
	userService             service.UserServiceInterface
	userIdentityService     service.UserIdentityServiceInterface
	authLogService          service.AuthLogServiceInterface
	identityProviderService service.AppIdentityProviderServiceInterface
	r                       service.InternalRegistry
	session                 service.SessionService
}

// NewOauthManager return new oauth manager.
func NewOauthManager(db database.MgoSession, r service.InternalRegistry, s *config.Session, h *config.Hydra) OauthManagerInterface {
	m := &OauthManager{
		hydraConfig:             h,
		r:                       r,
		userService:             service.NewUserService(db),
		userIdentityService:     service.NewUserIdentityService(db),
		authLogService:          service.NewAuthLogService(db),
		identityProviderService: service.NewAppIdentityProviderService(),
		session:                 service.NewSessionService(s.Name),
	}

	return m
}

func (m *OauthManager) CheckAuth(ctx echo.Context, form *models.Oauth2LoginForm) (string, *models.User, []*models.AppIdentityProvider, string, *models.GeneralError) {
	req, err := m.r.HydraAdminApi().GetLoginRequest(&admin.GetLoginRequestParams{Challenge: form.Challenge, Context: ctx.Request().Context()})
	if err != nil {
		return "", nil, nil, "", &models.GeneralError{Code: "common", Message: models.ErrorLoginChallenge, Err: errors.Wrap(err, "Unable to get client from login request")}
	}

	app, err := m.r.ApplicationService().Get(bson.ObjectIdHex(req.Payload.Client.ClientID))
	if err != nil {
		return "", nil, nil, "", &models.GeneralError{Code: "client_id", Message: models.ErrorClientIdIncorrect, Err: errors.Wrap(err, "Unable to load application")}
	}

	ipc := m.identityProviderService.FindByType(app, models.AppIdentityProviderTypeSocial)

	if err := m.session.Set(ctx, clientIdSessionKey, req.Payload.Client.ClientID); err != nil {
		return "", nil, nil, "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Error saving session")}
	}

	if req.Payload.Subject == "" {
		return req.Payload.Client.ClientID, nil, ipc, "", nil
	}

	if err := m.session.Set(ctx, loginRememberKey, req.Payload.Skip == true); err != nil {
		return "", nil, nil, "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Error saving session")}
	}

	if req.Payload.Skip == true {
		reqACL, err := m.r.HydraAdminApi().AcceptLoginRequest(&admin.AcceptLoginRequestParams{
			Context:   ctx.Request().Context(),
			Challenge: form.Challenge,
			Body:      &models2.HandledLoginRequest{Subject: &req.Payload.Subject},
		})
		if err != nil {
			return req.Payload.Client.ClientID, nil, nil, "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to accept login challenge")}
		}

		return req.Payload.Client.ClientID, nil, nil, reqACL.Payload.RedirectTo, nil
	}

	user, err := m.userService.Get(bson.ObjectIdHex(req.Payload.Subject))
	if err != nil {
		return req.Payload.Client.ClientID, nil, nil, "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to get user")}
	}

	return req.Payload.Client.ClientID, user, ipc, "", nil
}

func (m *OauthManager) Auth(ctx echo.Context, form *models.Oauth2LoginSubmitForm) (string, *models.GeneralError) {
	req, err := m.r.HydraAdminApi().GetLoginRequest(&admin.GetLoginRequestParams{Context: ctx.Request().Context(), Challenge: form.Challenge})
	if err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorLoginChallenge, Err: errors.Wrap(err, "Unable to get client from login request")}
	}

	userId := req.Payload.Subject
	userIdentity := &models.UserIdentity{}
	if req.Payload.Subject == "" || req.Payload.Subject != form.PreviousLogin {
		if form.Token != "" {
			if err := m.r.OneTimeTokenService().Use(form.Token, userIdentity); err != nil {
				return "", &models.GeneralError{Code: "common", Message: models.ErrorCannotUseToken, Err: errors.Wrap(err, "Unable to use OneTimeToken")}
			}
		} else {
			app, err := m.r.ApplicationService().Get(bson.ObjectIdHex(req.Payload.Client.ClientID))
			if err != nil {
				return "", &models.GeneralError{Code: "client_id", Message: models.ErrorClientIdIncorrect, Err: errors.Wrap(err, "Unable to load application")}
			}

			ipc := m.identityProviderService.FindByTypeAndName(app, models.AppIdentityProviderTypePassword, models.AppIdentityProviderNameDefault)
			if ipc == nil {
				return "", &models.GeneralError{Code: "client_id", Message: models.ErrorClientIdIncorrect, Err: errors.New("Unable to get identity provider")}
			}

			userIdentity, err = m.userIdentityService.Get(app, ipc, form.Email)
			if err != nil {
				return "", &models.GeneralError{Code: "email", Message: models.ErrorLoginIncorrect, Err: errors.Wrap(err, "Unable to get user identity")}
			}

			encryptor := models.NewBcryptEncryptor(&models.CryptConfig{Cost: app.PasswordSettings.BcryptCost})
			if err := encryptor.Compare(userIdentity.Credential, form.Password); err != nil {
				return "", &models.GeneralError{Code: "password", Message: models.ErrorPasswordIncorrect, Err: errors.Wrap(err, "Bad user password")}
			}
		}

		user, err := m.userService.Get(userIdentity.UserID)
		if err != nil {
			return "", &models.GeneralError{Code: "email", Message: models.ErrorLoginIncorrect, Err: errors.Wrap(err, "Unable to get user")}
		}

		user.LoginsCount = user.LoginsCount + 1
		if err := m.userService.Update(user); err != nil {
			return "", &models.GeneralError{Code: "common", Message: models.ErrorUpdateUser, Err: errors.Wrap(err, "Unable to update user")}
		}

		if err := m.authLogService.Add(ctx.RealIP(), ctx.Request().UserAgent(), user); err != nil {
			return "", &models.GeneralError{Code: "common", Message: models.ErrorAddAuthLog, Err: errors.Wrap(err, "Unable to add auth log")}
		}
		userId = user.ID.Hex()
	} else {
		form.Remember = true
	}

	if err := m.session.Set(ctx, loginRememberKey, form.Remember); err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Error saving session")}
	}

	// TODO: Add MFA cases

	reqACL, err := m.r.HydraAdminApi().AcceptLoginRequest(&admin.AcceptLoginRequestParams{
		Context:   ctx.Request().Context(),
		Challenge: form.Challenge,
		Body:      &models2.HandledLoginRequest{Subject: &userId, Remember: form.Remember, RememberFor: 0},
	})
	if err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorPasswordIncorrect, Err: errors.Wrap(err, "Unable to accept login challenge")}
	}

	return reqACL.Payload.RedirectTo, nil
}

func (m *OauthManager) Consent(ctx echo.Context, form *models.Oauth2ConsentForm) ([]string, *models.GeneralError) {
	scopes, err := m.GetScopes()
	reqGCR, err := m.r.HydraAdminApi().GetConsentRequest(&admin.GetConsentRequestParams{Context: ctx.Request().Context(), Challenge: form.Challenge})
	if err != nil {
		return scopes, &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to get consent challenge")}
	}

	if err := m.session.Set(ctx, clientIdSessionKey, reqGCR.Payload.Client.ClientID); err != nil {
		return scopes, &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Error saving session")}
	}

	return scopes, nil
}

func (m *OauthManager) ConsentSubmit(ctx echo.Context, form *models.Oauth2ConsentSubmitForm) (string, *models.GeneralError) {
	reqGCR, err := m.r.HydraAdminApi().GetConsentRequest(&admin.GetConsentRequestParams{Context: ctx.Request().Context(), Challenge: form.Challenge})
	if err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to get consent challenge")}
	}

	user, err := m.userService.Get(bson.ObjectIdHex(reqGCR.Payload.Subject))
	if err != nil {
		return "", &models.GeneralError{Code: "email", Message: models.ErrorLoginIncorrect, Err: errors.Wrap(err, "Unable to get user")}
	}

	remember := true
	if reqGCR.Payload.Skip == true {
		r, err := m.session.Get(ctx, loginRememberKey)
		if err != nil {
			return "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to get session")}
		}
		remember = r.(bool)
	}

	userInfo := map[string]interface{}{
		"email":                 user.Email,
		"email_verified":        user.EmailVerified,
		"phone_number":          user.PhoneNumber,
		"phone_number_verified": user.PhoneVerified,
		"name":                  user.Name,
		"picture":               user.Picture,
	}
	req := models2.HandledConsentRequest{
		GrantedScope: form.Scope,
		Session: &models2.ConsentRequestSessionData{
			IDToken:     userInfo,
			AccessToken: map[string]interface{}{"remember": remember}},
	}
	reqACR, err := m.r.HydraAdminApi().AcceptConsentRequest(&admin.AcceptConsentRequestParams{Context: ctx.Request().Context(), Challenge: form.Challenge, Body: &req})
	if err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to accept consent challenge")}
	}

	return reqACR.Payload.RedirectTo, nil
}

func (m *OauthManager) GetScopes() (scopes []string, err error) {
	scopes = []string{"openid", "offline"}
	/*if err := m.loadRemoteScopes(scopes); err != nil {
		return nil, err
	}*/

	return scopes, nil
}

func (m *OauthManager) Introspect(ctx echo.Context, form *models.Oauth2IntrospectForm) (*models.Oauth2TokenIntrospection, *models.GeneralError) {
	app, err := m.r.ApplicationService().Get(bson.ObjectIdHex(form.ClientID))
	if err != nil {
		return nil, &models.GeneralError{Code: "client_id", Message: models.ErrorClientIdIncorrect, Err: errors.Wrap(err, "Unable to load application")}
	}

	if app.AuthSecret != form.Secret {
		return nil, &models.GeneralError{Code: "secret", Message: models.ErrorUnknownError, Err: errors.New("Invalid secret key")}
	}

	client, err := m.r.HydraAdminApi().IntrospectOAuth2Token(&admin.IntrospectOAuth2TokenParams{Context: ctx.Request().Context(), Token: form.Token}, nil)
	if err != nil {
		return nil, &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to introspect token")}
	}

	token := &models.Oauth2TokenIntrospection{}
	if err := copier.Copy(&token, client.Payload); err != nil {
		return nil, &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to copy token")}
	}

	return token, nil
}

func (m *OauthManager) SignUp(ctx echo.Context, form *models.Oauth2SignUpForm) (string, *models.GeneralError) {
	if err := m.session.Set(ctx, loginRememberKey, form.Remember); err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Error saving session")}
	}

	clientId, err := m.session.Get(ctx, clientIdSessionKey)
	if err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to get session")}
	}

	app, err := m.r.ApplicationService().Get(bson.ObjectIdHex(clientId.(string)))
	if err != nil {
		return "", &models.GeneralError{Code: "client_id", Message: models.ErrorClientIdIncorrect, Err: errors.Wrap(err, "Unable to load application")}
	}
	if false == validator.IsPasswordValid(app, form.Password) {
		return "", &models.GeneralError{Code: "password", Message: models.ErrorPasswordIncorrect, Err: errors.New(models.ErrorPasswordIncorrect)}
	}

	encryptedPassword := ""
	t, _ := tomb.WithContext(ctx.Request().Context())
	t.Go(func() error {
		encryptor := models.NewBcryptEncryptor(&models.CryptConfig{Cost: app.PasswordSettings.BcryptCost})
		encryptedPassword, err = encryptor.Digest(form.Password)
		return err
	})

	req, err := m.r.HydraAdminApi().GetLoginRequest(&admin.GetLoginRequestParams{Context: ctx.Request().Context(), Challenge: form.Challenge})
	if err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorLoginChallenge, Err: errors.Wrap(err, "Unable to get client from login request")}
	}
	if req.Payload.Client.ClientID != clientId.(string) {
		return "", &models.GeneralError{Code: "client_id", Message: models.ErrorClientIdIncorrect, Err: errors.Wrap(err, "Client ID is incorrect")}
	}

	ipc := m.identityProviderService.FindByTypeAndName(app, models.AppIdentityProviderTypePassword, models.AppIdentityProviderNameDefault)
	if ipc == nil {
		return "", &models.GeneralError{Code: "client_id", Message: models.ErrorProviderIdIncorrect, Err: errors.New("Unable to get identity provider")}
	}

	userIdentity, err := m.userIdentityService.Get(app, ipc, form.Email)
	if err == nil {
		return "", &models.GeneralError{Code: "email", Message: models.ErrorLoginIncorrect, Err: errors.Wrap(err, "Unable to get user with identity for application")}
	}

	if err := t.Wait(); err != nil {
		return "", &models.GeneralError{Code: "password", Message: models.ErrorCryptPassword, Err: errors.Wrap(err, "Unable to crypt password")}
	}

	user := &models.User{
		ID:            bson.NewObjectId(),
		AppID:         app.ID,
		Email:         form.Email,
		EmailVerified: false,
		Blocked:       false,
		LastIp:        ctx.RealIP(),
		LastLogin:     time.Now(),
		LoginsCount:   1,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := m.userService.Create(user); err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorCreateUser, Err: errors.Wrap(err, "Unable to create user")}
	}

	userIdentity = &models.UserIdentity{
		ID:                 bson.NewObjectId(),
		UserID:             user.ID,
		ApplicationID:      app.ID,
		ExternalID:         form.Email,
		IdentityProviderID: ipc.ID,
		Credential:         encryptedPassword,
		Email:              form.Email,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	if err := m.userIdentityService.Create(userIdentity); err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorCreateUserIdentity, Err: errors.Wrap(err, "Unable to create user identity")}
	}

	if err := m.authLogService.Add(ctx.RealIP(), ctx.Request().UserAgent(), user); err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorAddAuthLog, Err: errors.Wrap(err, "Unable to add auth log")}
	}

	userId := user.ID.Hex()
	reqACL, err := m.r.HydraAdminApi().AcceptLoginRequest(&admin.AcceptLoginRequestParams{Context: ctx.Request().Context(), Challenge: form.Challenge, Body: &models2.HandledLoginRequest{Subject: &userId}})
	if err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to accept login challenge")}
	}

	return reqACL.Payload.RedirectTo, nil
}

func (m *OauthManager) CallBack(ctx echo.Context, form *models.Oauth2CallBackForm) (*models.Oauth2CallBackResponse, *models.GeneralError) {
	clientId, err := m.session.Get(ctx, clientIdSessionKey)
	if err != nil {
		return &models.Oauth2CallBackResponse{
				Success:      false,
				ErrorMessage: "unknown_client_id",
			}, &models.GeneralError{
				Code:    "client_id",
				Message: "Unable to get session",
				Err:     errors.Wrap(err, "Unable to get session"),
			}
	}

	if clientId == "" || clientId == nil {
		return &models.Oauth2CallBackResponse{
				Success:      false,
				ErrorMessage: "unknown_client_id",
			}, &models.GeneralError{
				Code:    "client_id",
				Message: "Unable to get client id from session",
				Err:     errors.New("Unable to get client id from session"),
			}
	}

	app, err := m.r.ApplicationService().Get(bson.ObjectIdHex(clientId.(string)))
	if err != nil {
		return &models.Oauth2CallBackResponse{
				Success:      false,
				ErrorMessage: "invalid_client_id",
			}, &models.GeneralError{
				Code:    "client_id",
				Message: models.ErrorClientIdIncorrect,
				Err:     errors.Wrap(err, "Unable to load application"),
			}
	}

	settings := jwtverifier.Config{
		ClientID:     clientId.(string),
		ClientSecret: app.AuthSecret,
		RedirectURL:  fmt.Sprintf("%s://%s/oauth2/callback", ctx.Scheme(), ctx.Request().Host),
		Issuer:       m.hydraConfig.PublicURL,
	}
	jwtv := jwtverifier.NewJwtVerifier(settings)
	tokens, err := jwtv.Exchange(ctx.Request().Context(), form.Code)
	if err != nil {
		return &models.Oauth2CallBackResponse{
				Success:      false,
				ErrorMessage: "unable_exchange_code",
			}, &models.GeneralError{
				Code:    "common",
				Message: models.ErrorUnknownError,
				Err:     errors.Wrap(err, "Unable to exchange code to token"),
			}
	}

	expIn := 0
	if tokens.AccessToken != "" {
		expIn = int(tokens.Expiry.Sub(time.Now()).Seconds())
	}

	return &models.Oauth2CallBackResponse{
		Success:     true,
		AccessToken: tokens.AccessToken,
		IdToken:     tokens.Extra("id_token").(string),
		ExpiresIn:   expIn,
	}, nil
}

func (m *OauthManager) Logout(ctx echo.Context, form *models.Oauth2LogoutForm) (string, *models.GeneralError) {
	logoutRedirectUri, err := m.session.Get(ctx, logoutSessionKey)
	if err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Unable to get session")}
	}

	if form.RedirectUri == "" {
		form.RedirectUri = "auth1"
	}
	if logoutRedirectUri == "" || logoutRedirectUri == nil {
		if err := m.session.Set(ctx, logoutSessionKey, form.RedirectUri); err != nil {
			return "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Error saving session")}
		}
		return logoutHydraUrl, nil
	}

	if err := m.session.Set(ctx, logoutSessionKey, ""); err != nil {
		return "", &models.GeneralError{Code: "common", Message: models.ErrorUnknownError, Err: errors.Wrap(err, "Error saving session")}
	}

	if logoutRedirectUri != "auth1" {
		return logoutRedirectUri.(string), nil
	}

	return "", nil
}

func (m *OauthManager) loadRemoteScopes(scopes []string) error {
	scopes = append(scopes, []string{"test1", "test2"}...)
	return nil
}
