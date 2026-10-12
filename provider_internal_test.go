package mail

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/lemmego/api/app"
	"github.com/lemmego/api/config"
)

// TestLogBodiesIsADecisionAndNeverFatal.
//
// The default differs by environment — in development the log is the only
// inbox there is, in production this transport should not be in use at all —
// so the one thing that must not happen is the setting deciding by accident.
// Reading it through config.MustEnv with a bool fallback would also panic on
// a value it cannot parse, taking a boot down over a logging preference.
func TestLogBodiesIsADecisionAndNeverFatal(t *testing.T) {
	cases := []struct {
		value    string
		fallback bool
		want     bool
		complain bool
	}{
		{value: "", fallback: true, want: true},
		{value: "", fallback: false, want: false},
		{value: "   ", fallback: true, want: true},
		{value: "true", fallback: false, want: true},
		{value: "false", fallback: true, want: false},
		{value: "TRUE", fallback: false, want: true},
		{value: "0", fallback: true, want: false},
		// Unreadable: the default, and said out loud, because a typo here
		// decides whether a working credential is written to a log.
		{value: "yes", fallback: false, want: false, complain: true},
		{value: "no", fallback: true, want: true, complain: true},
	}

	for _, c := range cases {
		t.Run("MAIL_LOG_BODIES="+c.value, func(t *testing.T) {
			t.Setenv("MAIL_LOG_BODIES", c.value)

			warnings := &bytes.Buffer{}
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(warnings, nil)))
			defer slog.SetDefault(previous)

			if got := logBodies(c.fallback); got != c.want {
				t.Errorf("logBodies(%v) = %v, want %v", c.fallback, got, c.want)
			}
			if complained := strings.Contains(warnings.String(), "MAIL_LOG_BODIES"); complained != c.complain {
				t.Errorf("complained = %v, want %v (log: %q)", complained, c.complain, warnings.String())
			}
		})
	}
}

// An absent variable is not the same as an empty one to os.LookupEnv, and
// both have to mean the default here.
func TestLogBodiesWithTheVariableAbsent(t *testing.T) {
	t.Setenv("MAIL_LOG_BODIES", "")
	if err := os.Unsetenv("MAIL_LOG_BODIES"); err != nil {
		t.Fatalf("unsetting: %v", err)
	}
	if !logBodies(true) {
		t.Error("an absent MAIL_LOG_BODIES did not take the default")
	}
	if logBodies(false) {
		t.Error("an absent MAIL_LOG_BODIES did not take the default")
	}
}

// TestTheDefaultTransportLogsBodiesOutsideProduction is the behaviour that
// matters: in development the log is the only inbox, so a verification email
// whose link is never printed looks like a broken feature.
//
// Asserted on the environment rather than on the setting, because the setting
// was already tested above and inverting this default passed every one of
// those tests.
func TestTheDefaultTransportLogsBodiesOutsideProduction(t *testing.T) {
	for _, c := range []struct {
		env  string
		want bool
	}{
		{env: "development", want: true},
		{env: "test", want: true},
		{env: "staging", want: true},
		{env: "production", want: false},
	} {
		t.Run(c.env, func(t *testing.T) {
			t.Setenv("MAIL_LOG_BODIES", "")
			if err := os.Unsetenv("MAIL_LOG_BODIES"); err != nil {
				t.Fatalf("unsetting: %v", err)
			}

			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
			defer slog.SetDefault(previous)

			transport, ok := defaultTransport(testApp(t, c.env)).(*LogTransport)
			if !ok {
				t.Fatal("the default transport is no longer the log transport")
			}
			if transport.Bodies != c.want {
				t.Errorf("Bodies = %v in %s, want %v", transport.Bodies, c.env, c.want)
			}
		})
	}
}

// And MAIL_LOG_BODIES still overrides it in either direction, which is the
// escape hatch for a staging box that must not log credentials and for a
// production incident where somebody has decided otherwise on purpose.
func TestMailLogBodiesOverridesTheEnvironmentDefault(t *testing.T) {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	defer slog.SetDefault(previous)

	t.Setenv("MAIL_LOG_BODIES", "false")
	if transport := defaultTransport(testApp(t, "development")).(*LogTransport); transport.Bodies {
		t.Error("MAIL_LOG_BODIES=false did not turn bodies off in development")
	}

	t.Setenv("MAIL_LOG_BODIES", "true")
	if transport := defaultTransport(testApp(t, "production")).(*LogTransport); !transport.Bodies {
		t.Error("MAIL_LOG_BODIES=true did not turn bodies on in production")
	}
}

func testApp(t *testing.T, env string) app.App {
	t.Helper()
	return app.Configure(app.WithConfig(config.M{
		"app": config.M{"name": "mail-test", "env": env, "port": 0},
	}))
}
