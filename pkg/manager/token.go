package manager

import (
	"auth-one-api/pkg/models"
	"github.com/sirupsen/logrus"
)

type TokenManager Config

func (m *TokenManager) Refresh(form *models.RefreshTokenForm) (token *models.AuthToken, error *models.CommonError) {
	return &models.AuthToken{
		RefreshToken: `refreshtoken`,
		AccessToken:  `accesstoken`,
		ExpiresIn:    1575983364,
	}, nil
}

func (m *TokenManager) OTT(form *models.OneTimeTokenForm) (token *models.AuthToken, error *models.CommonError) {
	if form.ClientId == `incorrect` {
		return nil, &models.CommonError{Code: `client_id`, Message: `Client ID is incorrect`}
	}
	if form.Token == `incorrect` {
		return nil, &models.CommonError{Code: `token`, Message: `Token is incorrect`}
	}

	return &models.AuthToken{
		RefreshToken: `refreshtoken`,
		AccessToken:  `accesstoken`,
		ExpiresIn:    1575983364,
	}, nil
}

func InitTokenManager(logger *logrus.Entry) TokenManager {
	m := TokenManager{
		Logger: logger,
	}

	return m
}