/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// CDEvents the recorder emits (spec v0.5.1). The CloudEvent id is the record
// digest; the concluded event has its own source, so (source, id) stays unique.
const (
	cdeventsVersion = "0.5.1"
	eventDeployed   = "dev.cdevents.service.deployed.0.3.0"
	eventUpgraded   = "dev.cdevents.service.upgraded.0.3.0"
	eventRolledBack = "dev.cdevents.service.rolledback.0.3.0"
	eventConcluded  = "dev.cdeventsx.pacto-operation.concluded.0.1.0"
	// A Stage created (its stream seeded) or deleted. The id is
	// <stage uid>/created|deleted and the subject the Stage's stream id.
	eventEnvironmentCreated = "dev.cdevents.environment.created.0.3.0"
	eventEnvironmentDeleted = "dev.cdevents.environment.deleted.0.3.0"
	eventPostDeadline       = 5 * time.Second
)

// Events posts CDEvents for the records the recorder writes, best effort: a
// sink that is slow or down is logged and never holds a record back.
type Events struct {
	// Sink is the URL CloudEvents are POSTed to, in structured mode.
	Sink string
	// Source is the CloudEvent source, such as /pacto-delivery/<cluster id>.
	Source string
	Client *http.Client
}

// emit sends the events of one newly written operation record in the
// background. A service event goes out only when the pointer moved and Flux
// applied it.
func (e *Events) emit(ctx context.Context, stage *dv1.Stage, st delivery.Statement, id string, applied bool, now time.Time) {
	if e == nil || st.Predicate.Kind != delivery.RecordOperation {
		return
	}
	events := cdEvents(e.Source, stage, st, id, applied, now)
	log := ctrl.LoggerFrom(ctx)
	go func() {
		for _, ev := range events {
			if err := e.post(ev); err != nil {
				log.Info("CDEvent not delivered", "record", id, "type", ev["type"], "error", err.Error())
			}
		}
	}()
}

// emitEnvironment posts the environment event of one Stage created or
// deleted, in the background, best effort.
func (e *Events) emitEnvironment(ctx context.Context, stage *dv1.Stage, created bool, now time.Time) {
	if e == nil {
		return
	}
	ev := environmentEvent(e.Source, stage, created, now)
	log := ctrl.LoggerFrom(ctx)
	go func() {
		if err := e.post(ev); err != nil {
			log.Info("CDEvent not delivered", "stage", stage.Namespace+"/"+stage.Name, "type", ev["type"], "error", err.Error())
		}
	}()
}

// environmentEvent is the environment.created or environment.deleted event of
// one Stage. The subject is the Stage's stream id, unique per Stage, so two
// Stages of one environment or two services of one name never collide; the
// CDEvents environment content holds only its name, so the service, domain and
// Stage go in customData. A delete with no deletionTimestamp has no time (see
// cloudEvent).
func environmentEvent(source string, stage *dv1.Stage, created bool, now time.Time) map[string]any {
	typ, suffix, at := eventEnvironmentCreated, "/created", stage.CreationTimestamp.UTC().Format(time.RFC3339)
	custom := map[string]any{"stage": stage.Namespace + "/" + stage.Name, "stageUID": string(stage.UID),
		"service": stage.Spec.Service.Name, "domain": stage.Spec.Service.Domain, "createdAt": at}
	if !created {
		typ, suffix, at = eventEnvironmentDeleted, "/deleted", ""
		if t := stage.DeletionTimestamp; t != nil {
			at = t.UTC().Format(time.RFC3339)
		}
	}
	return cloudEvent(source, string(stage.UID)+suffix, typ, at, now, map[string]any{
		"id": stage.Status.Stream.ID, "source": source, "content": map[string]any{"name": stage.Spec.Environment},
	}, custom)
}

func (e *Events) post(ev map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), eventPostDeadline)
	defer cancel()
	body, _ := json.Marshal(ev) // plain data; marshalling cannot fail
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Sink, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/cloudevents+json")
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: eventPostDeadline}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the sink answered %s", resp.Status)
	}
	return nil
}

// cdEvents builds the CloudEvents of one record: the concluded event always,
// and a service event when the pointer moved and Flux applied it.
func cdEvents(source string, stage *dv1.Stage, st delivery.Statement, id string, applied bool, now time.Time) []map[string]any {
	p := st.Predicate
	typ := ""
	if p.Type != nil {
		typ = p.Type.ID
	}
	concluded := cloudEvent(source+"/records", id, eventConcluded, p.Time.Finished, now, map[string]any{
		"id": p.Chain.Stream, "source": source, "type": "pacto-operation",
		"content": map[string]any{"record": id, "operation": p.Intent.Name, "operationType": typ, "seq": p.Chain.Seq,
			"outcome": p.Outcome.Result, "verification": p.Outcome.Verification, "stage": stage.Namespace + "/" + stage.Name},
	}, nil)
	out := append(make([]map[string]any, 0, 2), concluded)
	ptr := p.Outcome.Pointer
	if !applied || ptr.After == "" {
		return out
	}
	kind := eventUpgraded
	switch {
	case typ == delivery.TypeRollback:
		kind = eventRolledBack
	case ptr.Before == "":
		kind = eventDeployed
	}
	service := stage.Spec.Service.Name
	return append(out, cloudEvent(source, id, kind, p.Time.Finished, now, map[string]any{
		"id": service, "source": source,
		"content": map[string]any{
			"environment": map[string]any{"id": stage.Spec.Environment, "source": source},
			"artifactId":  "pkg:oci/" + service + "@" + ptr.After,
		},
	}, nil))
}

// cloudEvent wraps one CDEvent (context.specversion and a date-time
// context.timestamp, both required by v0.5.1). When the occurrence time at is
// unknown the CloudEvent carries no time, never a made-up one, and the
// required CDEvents timestamp is now, the moment the recorder saw it, which
// customData.observedAt states.
func cloudEvent(source, id, typ, at string, now time.Time, subject, custom map[string]any) map[string]any {
	ce := map[string]any{"specversion": "1.0", "id": id, "source": source, "type": typ, "datacontenttype": "application/json"}
	stamp := at
	if at == "" {
		stamp = now.UTC().Format(time.RFC3339)
		if custom == nil {
			custom = map[string]any{}
		}
		custom["observedAt"] = stamp
	} else {
		ce["time"] = at
	}
	data := map[string]any{
		"context": map[string]any{"specversion": cdeventsVersion, "id": id, "source": source, "type": typ, "timestamp": stamp},
		"subject": subject,
	}
	if custom != nil {
		data["customData"] = custom
	}
	ce["data"] = data
	return ce
}
