package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"backend/internal/config"
	"backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Keep these tests independent of config loading, middleware, database
// connections, and email services. A nil DB also catches unexpected work
// when a deadline guard should have returned immediately.
func newTeamQuestionsDeadlineTestHandler(t *testing.T, now time.Time) *TeamQuestionsHandler {
	t.Helper()
	return &TeamQuestionsHandler{
		cfg: &config.Config{FrontendURL: "https://example.com"},
		now: func() time.Time { return now },
		sendFinalCall: func(models.GeneralApplication, string, string, string) error {
			t.Error("unexpected final call send")
			return errors.New("unexpected final call send")
		},
	}
}

func TestTeamQuestionsOrdinarySendWindow(t *testing.T) {
	cases := []struct {
		name      string
		now       time.Time
		automatic error
		manual    error
	}{
		{name: "before final calls", now: teamQuestionsFinalCallStart.Add(-time.Second)},
		{name: "at final-call start", now: teamQuestionsFinalCallStart, automatic: errTeamQuestionsOrdinarySendEnded},
		{name: "last second of September 8", now: teamQuestionsSubmissionCutoff.Add(-time.Second), automatic: errTeamQuestionsOrdinarySendEnded},
		{name: "at closure", now: teamQuestionsSubmissionCutoff, automatic: errTeamQuestionsClosed, manual: errTeamQuestionsClosed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTeamQuestionsDeadlineTestHandler(t, tc.now)
			require.ErrorIs(t, h.ordinarySendWindowError(true), tc.automatic)
			require.ErrorIs(t, h.ordinarySendWindowError(false), tc.manual)
		})
	}
}

func TestTeamQuestionsOrdinaryAutomaticSendsStopAtFinalCallStart(t *testing.T) {
	for _, now := range []time.Time{
		teamQuestionsFinalCallStart,
		teamQuestionsSubmissionCutoff.Add(-time.Second),
		teamQuestionsSubmissionCutoff,
	} {
		t.Run(now.Format(time.RFC3339), func(t *testing.T) {
			h := newTeamQuestionsDeadlineTestHandler(t, now)
			for name, send := range map[string]func() (int, []string, error){
				"invites":   h.SendPendingInvites,
				"reminders": h.SendPendingReminders,
			} {
				t.Run(name, func(t *testing.T) {
					sent, failed, err := send()
					require.NoError(t, err)
					require.Zero(t, sent)
					require.Empty(t, failed)
				})
			}
		})
	}
}

func TestTeamQuestionsOrdinarySendGuardsBeforeDatabaseAccess(t *testing.T) {
	application := models.GeneralApplication{Id: uuid.New(), Email: "applicant@example.com"}
	h := newTeamQuestionsDeadlineTestHandler(t, teamQuestionsFinalCallStart)
	require.ErrorIs(t, h.issueAndSendOrdinary(application, "", "", false, true), errTeamQuestionsOrdinarySendEnded)
	require.ErrorIs(t, h.issueAndSendReminder(application, "", ""), errTeamQuestionsOrdinarySendEnded)

	h = newTeamQuestionsDeadlineTestHandler(t, teamQuestionsSubmissionCutoff)
	require.ErrorIs(t, h.issueAndSend(application, "", ""), errTeamQuestionsClosed)
	require.ErrorIs(t, h.issueAndSendOrdinary(application, "", "", false, true), errTeamQuestionsClosed)
	require.ErrorIs(t, h.issueAndSendReminder(application, "", ""), errTeamQuestionsClosed)
}

func TestTeamQuestionsFinalCallsOnlyRunInsideWindow(t *testing.T) {
	for _, now := range []time.Time{
		teamQuestionsFinalCallStart.Add(-time.Second),
		teamQuestionsSubmissionCutoff,
		teamQuestionsSubmissionCutoff.Add(24 * time.Hour),
	} {
		t.Run(now.Format(time.RFC3339), func(t *testing.T) {
			h := newTeamQuestionsDeadlineTestHandler(t, now)
			sent, failed, err := h.SendPendingFinalCalls()
			require.NoError(t, err)
			require.Zero(t, sent)
			require.Empty(t, failed)
			delivered, err := h.issueAndSendFinalCall(uuid.New())
			require.NoError(t, err)
			require.False(t, delivered)
		})
	}
}

func TestTeamQuestionsDevelopmentFinalCallSkipDoesNotSend(t *testing.T) {
	h := newTeamQuestionsDeadlineTestHandler(t, teamQuestionsFinalCallStart)
	h.cfg.DevelopmentMode = true
	sent, failed, err := h.SendPendingFinalCalls()
	require.NoError(t, err)
	require.Zero(t, sent)
	require.Empty(t, failed)
	delivered, err := h.issueAndSendFinalCall(uuid.New())
	require.NoError(t, err)
	require.False(t, delivered)
}

func TestTeamQuestionsClosedHTTPGuards(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		route   string
		path    string
		body    string
		handler func(*TeamQuestionsHandler, *gin.Context)
	}{
		{name: "GET missing token", method: http.MethodGet, route: "/form", path: "/form", handler: (*TeamQuestionsHandler).GetForm},
		{name: "GET malformed token", method: http.MethodGet, route: "/form/:token", path: "/form/not-a-token", handler: (*TeamQuestionsHandler).GetForm},
		{name: "POST missing token and body", method: http.MethodPost, route: "/form", path: "/form", handler: (*TeamQuestionsHandler).SubmitForm},
		{name: "POST malformed token and body", method: http.MethodPost, route: "/form/:token", path: "/form/not-a-token", body: "{", handler: (*TeamQuestionsHandler).SubmitForm},
		{name: "resend missing id", method: http.MethodPost, route: "/resend", path: "/resend", handler: (*TeamQuestionsHandler).AdminResend},
		{name: "resend malformed id", method: http.MethodPost, route: "/resend/:id", path: "/resend/not-a-uuid", body: "{", handler: (*TeamQuestionsHandler).AdminResend},
		{name: "bulk missing identity and body", method: http.MethodPost, route: "/bulk", path: "/bulk", handler: (*TeamQuestionsHandler).AdminSendBulk},
		{name: "bulk malformed body", method: http.MethodPost, route: "/bulk", path: "/bulk", body: "{", handler: (*TeamQuestionsHandler).AdminSendBulk},
	}
	for _, now := range []time.Time{teamQuestionsSubmissionCutoff, teamQuestionsSubmissionCutoff.Add(24 * time.Hour)} {
		t.Run(now.Format(time.RFC3339), func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					h := newTeamQuestionsDeadlineTestHandler(t, now)
					router := gin.New()
					router.Handle(tc.method, tc.route, func(c *gin.Context) { tc.handler(h, c) })
					request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					require.Equal(t, http.StatusGone, response.Code)
					require.JSONEq(t, `{"error":"team questions submissions closed after September 8, 2026 (Europe/Stockholm)"}`, response.Body.String())
				})
			}
		})
	}
}

func TestTeamQuestionsBlankTokensRemainInvalidBeforeClosure(t *testing.T) {
	for _, now := range []time.Time{teamQuestionsFinalCallStart.Add(-time.Second), teamQuestionsSubmissionCutoff.Add(-time.Second)} {
		t.Run(now.Format(time.RFC3339), func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				t.Run(method, func(t *testing.T) {
					h := newTeamQuestionsDeadlineTestHandler(t, now)
					router := gin.New()
					router.GET("/form", h.GetForm)
					router.POST("/form", h.SubmitForm)
					response := httptest.NewRecorder()
					router.ServeHTTP(response, httptest.NewRequest(method, "/form", nil))
					require.Equal(t, http.StatusNotFound, response.Code)
					require.JSONEq(t, `{"error":"this link is invalid or has expired"}`, response.Body.String())
				})
			}
		})
	}
}
