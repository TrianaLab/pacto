package diff

import (
	"fmt"

	"github.com/trianalab/pacto/pkg/contract"
)

// diffContract compares root-level fields: service identity, scaling, metadata.
func diffContract(old, new *contract.Contract) []Change {
	var changes []Change

	// Service identity
	if old.Service.Name != new.Service.Name {
		changes = append(changes, newChange("service.name", Modified, old.Service.Name, new.Service.Name))
	}
	if old.Service.Version != new.Service.Version {
		changes = append(changes, newChange("service.version", Modified, old.Service.Version, new.Service.Version))
	}
	if !old.Service.Owner.Equal(new.Service.Owner) {
		ct := Modified
		if old.Service.Owner.IsEmpty() {
			ct = Added
		} else if new.Service.Owner.IsEmpty() {
			ct = Removed
		}
		changes = append(changes, newChange("service.owner", ct, old.Service.Owner.DisplayString(), new.Service.Owner.DisplayString()))
	}

	// Image
	oldImg := formatImage(old.Service.Image)
	newImg := formatImage(new.Service.Image)
	if oldImg != newImg {
		ct := Modified
		if old.Service.Image == nil {
			ct = Added
		} else if new.Service.Image == nil {
			ct = Removed
		}
		changes = append(changes, newChange("service.image", ct, oldImg, newImg))
	}

	// Chart
	oldChart := formatChart(old.Service.Chart)
	newChart := formatChart(new.Service.Chart)
	if oldChart != newChart {
		ct := Modified
		if old.Service.Chart == nil {
			ct = Added
		} else if new.Service.Chart == nil {
			ct = Removed
		}
		changes = append(changes, newChange("service.chart", ct, oldChart, newChart))
	}

	// Scaling
	changes = append(changes, diffScaling(old.Scaling, new.Scaling)...)

	return changes
}

func diffScaling(old, new *contract.Scaling) []Change {
	var changes []Change

	if old == nil && new == nil {
		return nil
	}
	if old == nil {
		changes = append(changes, newChange("scaling", Added, nil, formatScaling(new)))
		return changes
	}
	if new == nil {
		changes = append(changes, newChange("scaling", Removed, formatScaling(old), nil))
		return changes
	}

	// Detect mode change (replicas vs min/max range).
	oldHasReplicas := old.Replicas != nil
	newHasReplicas := new.Replicas != nil

	if oldHasReplicas != newHasReplicas {
		changes = append(changes, newChange("scaling", Modified, formatScaling(old), formatScaling(new)))
		return changes
	}

	if oldHasReplicas {
		if *old.Replicas != *new.Replicas {
			changes = append(changes, newChange("scaling.replicas", Modified, *old.Replicas, *new.Replicas))
		}
		return changes
	}

	if old.Min != new.Min {
		changes = append(changes, newChange("scaling.min", Modified, old.Min, new.Min))
	}
	if old.Max != new.Max {
		changes = append(changes, newChange("scaling.max", Modified, old.Max, new.Max))
	}

	return changes
}

func formatScaling(s *contract.Scaling) string {
	if s.Replicas != nil {
		return fmt.Sprintf("replicas=%d", *s.Replicas)
	}
	return fmt.Sprintf("min=%d max=%d", s.Min, s.Max)
}

func formatImage(img *contract.Image) string {
	if img == nil {
		return ""
	}
	return img.Ref
}

func formatChart(ch *contract.Chart) string {
	if ch == nil {
		return ""
	}
	return fmt.Sprintf("%s:%s", ch.Ref, ch.Version)
}

// newChange creates a Change with classification looked up from the rules table.
func newChange(path string, ct ChangeType, oldVal, newVal any) Change {
	cls := classify(path, ct)
	return Change{
		Path:           path,
		Type:           ct,
		OldValue:       oldVal,
		NewValue:       newVal,
		Classification: cls,
		Reason:         fmt.Sprintf("%s %s", path, ct),
	}
}

// diffStringSet compares two string-keyed boolean maps and emits Added/Removed
// changes. pathPrefix is used for the classification rule lookup (e.g.
// "openapi.paths"), and entityName for human-readable reasons (e.g. "API path").
func diffStringSet(oldSet, newSet map[string]bool, pathPrefix, entityName string) []Change {
	var changes []Change

	for key := range oldSet {
		if !newSet[key] {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("%s[%s]", pathPrefix, key),
				Type:           Removed,
				OldValue:       key,
				Classification: classify(pathPrefix, Removed),
				Reason:         fmt.Sprintf("%s %s removed", entityName, key),
			})
		}
	}

	for key := range newSet {
		if !oldSet[key] {
			changes = append(changes, Change{
				Path:           fmt.Sprintf("%s[%s]", pathPrefix, key),
				Type:           Added,
				NewValue:       key,
				Classification: classify(pathPrefix, Added),
				Reason:         fmt.Sprintf("%s %s added", entityName, key),
			})
		}
	}

	return changes
}
