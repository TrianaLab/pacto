package detect

// EvidencePiece is the profile idp Evidence Server row: state (on, off or
// unknown) and why, the objects it read (the trust Secret goes there, never
// in why) and the detection's completeness and reason.
func EvidencePiece(state, why string, objects []Object, completeness, reason string) Entry {
	return Entry{Kind: "evidence", Value: state, Evidence: why, Source: "operator evidence gate",
		Completeness: completeness, Reason: reason, Objects: objects}
}

// RecorderStates are the values of the operator's --idp-recorder flag: what
// the chart decided about the delivery recorder under profile idp.
var RecorderStates = []string{"on", "off", "no-signer", "no-streams", "signer-incomplete", "delivery-off"}

// recorderWhy says why the recorder is in each of RecorderStates.
var recorderWhy = map[string]string{
	"on":                "signs every concluded operation",
	"off":               "delivery.recorder.enabled is false (an upgrade with --reuse-values carries the false an earlier chart defaulted to); set it to null to let profile idp decide",
	"no-signer":         "no signing method is set; set one of delivery.recorder.signing",
	"no-streams":        "no streams repository; set registry",
	"signer-incomplete": "the signing method fails a recorder check; see helm template with delivery.recorder.enabled=true",
	"delivery-off":      "profile hardened renders no Pacto delivery",
}

// RecorderPiece is the profile idp recorder row of state, one of
// RecorderStates: on, or off and why. Any other state is unknown.
func RecorderPiece(state string) Entry {
	why, ok := recorderWhy[state]
	if !ok {
		return UnknownEntry("recorder", "unknown recorder state "+state)
	}
	value := "off"
	if state == "on" {
		value = "on"
	}
	return Entry{Kind: "recorder", Value: value, Evidence: why, Source: "chart values", Completeness: Complete}
}
