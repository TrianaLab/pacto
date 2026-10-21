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
	cdeventsVersion   = "0.5.1"
	eventDeployed     = "dev.cdevents.service.deployed.0.3.0"
	eventUpgraded     = "dev.cdevents.service.upgraded.0.3.0"
	eventRolledBack   = "dev.cdevents.service.rolledback.0.3.0"
	eventConcluded    = "dev.cdeventsx.pacto-operation.concluded.0.1.0"
	eventPostDeadline = 5 * time.Second
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
func (e *Events) emit(ctx context.Context, stage *dv1.Stage, st delivery.Statement, id string, applied bool) {
	if e == nil || st.Predicate.Kind != delivery.RecordOperation {
		return
	}
	events := cdEvents(e.Source, stage, st, id, applied)
	log := ctrl.LoggerFrom(ctx)
	go func() {
		for _, ev := range events {
			if err := e.post(ev); err != nil {
				log.Info("CDEvent not delivered", "record", id, "type", ev["type"], "error", err.Error())
			}
		}
	}()
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
func cdEvents(source string, stage *dv1.Stage, st delivery.Statement, id string, applied bool) []map[string]any {
	p := st.Predicate
	typ := ""
	if p.Type != nil {
		typ = p.Type.ID
	}
	concluded := cloudEvent(source+"/records", id, eventConcluded, p.Time.Finished, map[string]any{
		"id": p.Chain.Stream, "source": source, "type": "pacto-operation",
		"content": map[string]any{"record": id, "operation": p.Intent.Name, "operationType": typ, "seq": p.Chain.Seq,
			"outcome": p.Outcome.Result, "verification": p.Outcome.Verification, "stage": stage.Namespace + "/" + stage.Name},
	})
	out := []map[string]any{concluded}
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
	return append(out, cloudEvent(source, id, kind, p.Time.Finished, map[string]any{
		"id": service, "source": source,
		"content": map[string]any{
			"environment": map[string]any{"id": stage.Spec.Environment, "source": source},
			"artifactId":  "pkg:oci/" + service + "@" + ptr.After,
		},
	}))
}

func cloudEvent(source, id, typ, at string, subject map[string]any) map[string]any {
	return map[string]any{
		"specversion": "1.0", "id": id, "source": source, "type": typ, "time": at,
		"datacontenttype": "application/json",
		"data": map[string]any{
			"context": map[string]any{"version": cdeventsVersion, "id": id, "source": source, "type": typ, "timestamp": at},
			"subject": subject,
		},
	}
}
