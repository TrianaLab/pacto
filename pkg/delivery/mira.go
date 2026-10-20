package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Mira's query API: the largest page it serves, and how many pages the gate
// reads before it gives up on an exact count.
// ponytail: Mira's /api/v1/query refuses limit:0 (count-only) on purpose, so
// the gate pages to the end, a ceiling of 200000 spans per window; switch to a
// count query once Mira offers one (vision open question 3).
const (
	miraPageSize = 10000
	miraMaxPages = 20
)

// Mira is the health gate over Mira's query API: the error ratio, and
// optionally the share of slow spans, of the service's traces in the window.
type Mira struct {
	URL    string
	Client *http.Client
}

// miraQuery is a Mira /api/v1/query document. Times are absolute nanoseconds,
// the form Mira returns and accepts back, so a cited query re-runs over
// exactly the same window.
type miraQuery struct {
	Signal string           `json:"signal"`
	From   int64            `json:"from"`
	To     int64            `json:"to"`
	Where  []map[string]any `json:"where"`
	Limit  int              `json:"limit"`
	After  string           `json:"after,omitempty"`
}

func (q miraQuery) with(term map[string]any) miraQuery {
	q.Where = append(slices.Clone(q.Where), term)
	return q
}

func (q miraQuery) String() string {
	b, _ := json.Marshal(q)
	return string(b)
}

type miraParams struct {
	minSpans                       int
	maxErrorRatio, maxLatencyRatio float64
	soak, latencyThreshold         time.Duration
	where                          []map[string]any
}

func parseMiraParams(p map[string]string) (miraParams, error) {
	out := miraParams{minSpans: 20, maxErrorRatio: 0.05, maxLatencyRatio: 0.05, soak: time.Minute}
	ratio := func(v *float64) func(string) error {
		return func(s string) error {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil || f < 0 || f > 1 {
				return errors.New("must be a ratio between 0 and 1")
			}
			*v = f
			return nil
		}
	}
	duration := func(v *time.Duration) func(string) error {
		return func(s string) error {
			d, err := time.ParseDuration(s)
			if err != nil || d <= 0 {
				return errors.New("must be a positive Go duration")
			}
			*v = d
			return nil
		}
	}
	setters := map[string]func(string) error{
		"minSpans": func(s string) error {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 {
				return errors.New("must be a positive integer")
			}
			out.minSpans = n
			return nil
		},
		"maxErrorRatio":    ratio(&out.maxErrorRatio),
		"maxLatencyRatio":  ratio(&out.maxLatencyRatio),
		"soak":             duration(&out.soak),
		"latencyThreshold": duration(&out.latencyThreshold),
		"where": func(s string) error {
			if err := json.Unmarshal([]byte(s), &out.where); err != nil {
				return errors.New("must be a JSON list of Mira terms")
			}
			return nil
		},
	}
	for _, k := range slices.Sorted(maps.Keys(p)) {
		set, ok := setters[k]
		if !ok {
			return out, fmt.Errorf("unknown param %q (known: %s)", k, strings.Join(slices.Sorted(maps.Keys(setters)), ", "))
		}
		if err := set(p[k]); err != nil {
			return out, fmt.Errorf("%s %w, not %q", k, err, p[k])
		}
	}
	return out, nil
}

// Check implements Gate. A read that fails or stops short is Unknown, never
// Pass; so is a window with fewer than minSpans spans. A confirmed breach is a
// Fail at once; a clean window passes only once it covers the soak time.
func (m Mira) Check(ctx context.Context, in GateInput, params map[string]string) Verdict {
	v := Verdict{Result: Unknown, Citation: map[string]string{}}
	p, err := parseMiraParams(params)
	if err != nil {
		v.Message = "invalid params: " + err.Error()
		return v
	}
	base := miraQuery{Signal: "traces", From: in.Window.From.UnixNano(), To: in.Window.To.UnixNano(), Limit: miraPageSize,
		Where: append([]map[string]any{{"attr": "service.name", "eq": in.Service.Name}}, p.where...)}
	c := v.Citation
	c["query"], c["from"], c["to"] = base.String(), ts3339(in.Window.From), ts3339(in.Window.To)
	c["minSpans"], c["soak"] = strconv.Itoa(p.minSpans), p.soak.String()
	c["maxErrorRatio"] = strconv.FormatFloat(p.maxErrorRatio, 'f', -1, 64)

	total, err := m.count(ctx, base)
	if err != nil {
		v.Message = "reading spans from Mira: " + err.Error()
		return v
	}
	c["spans"] = strconv.Itoa(total)
	if total < p.minSpans {
		v.Message = fmt.Sprintf("only %d spans in the window; at least %d are needed to judge", total, p.minSpans)
		return v
	}
	errQ := base.with(map[string]any{"field": "status_code", "eq": 2})
	c["errorQuery"] = errQ.String()
	errs, err := m.count(ctx, errQ)
	if err != nil {
		v.Message = "reading failed spans from Mira: " + err.Error()
		return v
	}
	c["errors"] = strconv.Itoa(errs)
	msgs := []string{share(errs, total, "failed", p.maxErrorRatio)}
	breach := ratioOf(errs, total) > p.maxErrorRatio
	if p.latencyThreshold > 0 {
		slowQ := base.with(map[string]any{"field": "duration_nano", "gt": p.latencyThreshold.Nanoseconds()})
		c["latencyQuery"], c["latencyThreshold"] = slowQ.String(), p.latencyThreshold.String()
		c["maxLatencyRatio"] = strconv.FormatFloat(p.maxLatencyRatio, 'f', -1, 64)
		slow, err := m.count(ctx, slowQ)
		if err != nil {
			v.Message = "reading slow spans from Mira: " + err.Error()
			return v
		}
		c["slow"] = strconv.Itoa(slow)
		msgs = append(msgs, share(slow, total, "took longer than "+p.latencyThreshold.String(), p.maxLatencyRatio))
		breach = breach || ratioOf(slow, total) > p.maxLatencyRatio
	}
	v.Message = strings.Join(msgs, "; ")
	switch soaked := in.Window.To.Sub(in.Window.From); {
	case breach:
		v.Result = Fail
	case soaked < p.soak:
		v.Message = fmt.Sprintf("soaking: judged after %s, %s so far (%s)", p.soak, soaked.Truncate(time.Second), v.Message)
	default:
		v.Result = Pass
	}
	return v
}

func ratioOf(n, total int) float64 { return float64(n) / float64(total) }

func share(n, total int, what string, limit float64) string {
	side := "within"
	if ratioOf(n, total) > limit {
		side = "above"
	}
	return fmt.Sprintf("%d of %d spans %s (%.2f%%), %s the %.2f%% limit", n, total, what, 100*ratioOf(n, total), side, 100*limit)
}

func ts3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// count pages through every span q matches and counts them.
func (m Mira) count(ctx context.Context, q miraQuery) (int, error) {
	u := strings.TrimSuffix(m.URL, "/") + "/api/v1/query"
	n := 0
	for range miraMaxPages {
		var page struct {
			Rows *[]json.RawMessage `json:"rows"`
			Next string             `json:"next"`
		}
		if err := postJSON(ctx, m.Client, u, q, &page); err != nil {
			return 0, err
		}
		if page.Rows == nil {
			return 0, fmt.Errorf("%s answered without rows", u)
		}
		n += len(*page.Rows)
		if page.Next == "" {
			return n, nil
		}
		q.After = page.Next
	}
	return 0, fmt.Errorf("more than %d spans in the window, more than the gate counts exactly through Mira's query API; narrow the filter", miraPageSize*miraMaxPages)
}
