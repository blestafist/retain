package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/pestit/retain/internal/auth"
	"github.com/pestit/retain/internal/stats"
)

// RegisterStatsRoutes registers authenticated, owner-scoped analytics endpoints.
func RegisterStatsRoutes(mux *http.ServeMux, authService *auth.Service, statsService *stats.Service) {
	mux.HandleFunc("GET /api/v1/stats/overview", statsOverviewHandler(authService, statsService))
	mux.HandleFunc("GET /api/v1/stats/forecast", statsForecastHandler(authService, statsService))
	mux.HandleFunc("GET /api/v1/stats/retention", statsRetentionHandler(authService, statsService))
	mux.HandleFunc("GET /api/v1/stats/heatmap", statsHeatmapHandler(authService, statsService))
	mux.HandleFunc("GET /api/v1/stats/cards/{id}/curve", statsCurveHandler(authService, statsService))
	mux.HandleFunc("GET /api/v1/stats/distribution", statsDistributionHandler(authService, statsService))
}

func statsOverviewHandler(authService *auth.Service, service *stats.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		if !validStatsQuery(w, r, nil) {
			return
		}
		result, err := service.Overview(r.Context(), user.ID)
		writeStatsResult(w, result, err)
	}
}

func statsForecastHandler(authService *auth.Service, service *stats.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		values, ok := statsQuery(w, r, "days")
		if !ok {
			return
		}
		days := 30
		if raw := values.Get("days"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 90 {
				writeStatsInputError(w)
				return
			}
			days = parsed
		}
		result, err := service.Forecast(r.Context(), user.ID, days)
		writeStatsResult(w, result, err)
	}
}

func statsRetentionHandler(authService *auth.Service, service *stats.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		values, ok := statsQuery(w, r, "period")
		if !ok {
			return
		}
		period := values.Get("period")
		if period == "" {
			period = "week"
		}
		result, err := service.Retention(r.Context(), user.ID, period)
		writeStatsResult(w, result, err)
	}
}

func statsHeatmapHandler(authService *auth.Service, service *stats.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		values, ok := statsQuery(w, r, "year")
		if !ok {
			return
		}
		year := 0
		if raw := values.Get("year"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				writeStatsInputError(w)
				return
			}
			year = parsed
		}
		result, err := service.Heatmap(r.Context(), user.ID, year)
		writeStatsResult(w, result, err)
	}
}

func statsCurveHandler(authService *auth.Service, service *stats.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		if !validStatsQuery(w, r, nil) {
			return
		}
		cardID, ok := contentID(w, r)
		if !ok {
			return
		}
		result, err := service.Curve(r.Context(), user.ID, cardID)
		writeStatsResult(w, result, err)
	}
}

func statsDistributionHandler(authService *auth.Service, service *stats.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := authenticatedUser(w, r, authService)
		if !ok {
			return
		}
		if !validStatsQuery(w, r, nil) {
			return
		}
		result, err := service.Distribution(r.Context(), user.ID)
		writeStatsResult(w, result, err)
	}
}

func statsQuery(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeStatsInputError(w)
		return nil, false
	}
	allowedKeys := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedKeys[key] = struct{}{}
	}
	for key, items := range values {
		if _, ok := allowedKeys[key]; !ok || len(items) != 1 {
			writeStatsInputError(w)
			return nil, false
		}
	}
	return values, true
}

func validStatsQuery(w http.ResponseWriter, r *http.Request, allowed []string) bool {
	_, ok := statsQuery(w, r, allowed...)
	return ok
}

func writeStatsResult(w http.ResponseWriter, result any, err error) {
	if err != nil {
		writeStatsError(w, err)
		return
	}
	if err := WriteJSON(w, http.StatusOK, result); err != nil {
		writeInternalError(w)
	}
}

func writeStatsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, stats.ErrInvalidInput):
		writeStatsInputError(w)
	case errors.Is(err, stats.ErrNotFound):
		_ = WriteAPIError(w, http.StatusNotFound, "not_found", "Resource not found.")
	default:
		writeInternalError(w)
	}
}

func writeStatsInputError(w http.ResponseWriter) {
	_ = WriteAPIError(w, http.StatusBadRequest, "invalid_input", "The stats request contains invalid input.")
}
