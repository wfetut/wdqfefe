package web

import (
	"net/http"

	"github.com/gravitational/trace"
	"github.com/julienschmidt/httprouter"

	"github.com/gravitational/teleport/api/gen/proto/go/userpreferences/v1"
	"github.com/gravitational/teleport/lib/assist/userpreferences"
	"github.com/gravitational/teleport/lib/httplib"
)

// getUserPreferences is a handler for GET /webapi/user/preferences
func (h *Handler) getUserPreferences(_ http.ResponseWriter, r *http.Request,
	p httprouter.Params, sctx *SessionContext,
) (any, error) {
	authClient, err := sctx.GetClient()
	if err != nil {
		return nil, trace.Wrap(err)
	}

	resp, err := authClient.GetUserPreferences(r.Context(), &userpreferencesv1.GetUserPreferencesRequest{
		Username: sctx.GetUser(),
	})
	if err != nil {
		return nil, trace.Wrap(err)
	}

	return userPreferencesResponse(resp), nil
}

// userPreferencesResponse creates a response for GET assistant settings.
func userPreferencesResponse(resp *userpreferencesv1.UserPreferences) any {
	type response struct {
		Assist userpreferences.AssistUserPreferencesResponse `json:"assist"`
		Theme  userpreferencesv1.Theme                       `json:"theme"`
	}

	jsonResp := &response{
		Assist: userpreferences.UserPreferencesResponse(resp.Assist),
		Theme:  resp.Theme,
	}

	return jsonResp
}

// updateUserPreferences is a handler for PUT /webapi/user/preferences.
func (h *Handler) updateUserPreferences(_ http.ResponseWriter, r *http.Request,
	p httprouter.Params, sctx *SessionContext,
) (any, error) {
	req := userpreferencesv1.UserPreferences{}

	if err := httplib.ReadJSON(r, &req); err != nil {
		return nil, trace.Wrap(err)
	}

	authClient, err := sctx.GetClient()
	if err != nil {
		return nil, trace.Wrap(err)
	}

	preferences := &userpreferencesv1.UpdateUserPreferencesRequest{
		Username:    sctx.GetUser(),
		Preferences: &req,
	}

	if err := authClient.UpdateUserPreferences(r.Context(), preferences); err != nil {
		return nil, trace.Wrap(err)
	}

	return OK(), nil
}
