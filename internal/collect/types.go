package collect

import (
	"github.com/UdayKandyanam/kubepulse/internal/snapshot"
)

// The types in this file mirror the parts of kubectl JSON output that
// Convert reads. They follow the Kubernetes API shapes, not the
// snapshot shape, so the conversion between the two stays in one
// place.

type metadataJSON struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type podListJSON struct {
	Items []podItemJSON `json:"items"`
}

type podItemJSON struct {
	Metadata metadataJSON `json:"metadata"`
	Spec     struct {
		NodeName   string `json:"nodeName"`
		Containers []struct {
			Name      string             `json:"name"`
			Image     string             `json:"image"`
			Resources snapshot.Resources `json:"resources"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase             string                `json:"phase"`
		Conditions        []snapshot.Condition  `json:"conditions,omitempty"`
		ContainerStatuses []containerStatusJSON `json:"containerStatuses,omitempty"`
	} `json:"status"`
}

type containerStatusJSON struct {
	Name         string                  `json:"name"`
	RestartCount int                     `json:"restartCount"`
	State        snapshot.ContainerState `json:"state"`
	LastState    snapshot.ContainerState `json:"lastState,omitempty"`
}

type deploymentListJSON struct {
	Items []struct {
		Metadata metadataJSON `json:"metadata"`
		Spec     struct {
			Replicas int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			UpdatedReplicas   int                  `json:"updatedReplicas"`
			ReadyReplicas     int                  `json:"readyReplicas"`
			AvailableReplicas int                  `json:"availableReplicas"`
			Conditions        []snapshot.Condition `json:"conditions,omitempty"`
		} `json:"status"`
	} `json:"items"`
}

type nodeListJSON struct {
	Items []struct {
		Metadata metadataJSON `json:"metadata"`
		Status   struct {
			Conditions []snapshot.Condition `json:"conditions,omitempty"`
		} `json:"status"`
	} `json:"items"`
}

type eventListJSON struct {
	Items []struct {
		Type           string `json:"type"`
		Reason         string `json:"reason"`
		InvolvedObject struct {
			Kind      string `json:"kind"`
			Namespace string `json:"namespace,omitempty"`
			Name      string `json:"name"`
		} `json:"involvedObject"`
		Message       string `json:"message"`
		Count         int    `json:"count,omitempty"`
		LastTimestamp string `json:"lastTimestamp,omitempty"`
		EventTime     string `json:"eventTime,omitempty"`
	} `json:"items"`
}

type jobListJSON struct {
	Items []struct {
		Metadata metadataJSON `json:"metadata"`
		Spec     struct {
			Completions int `json:"completions,omitempty"`
		} `json:"spec"`
		Status struct {
			Active     int                  `json:"active,omitempty"`
			Succeeded  int                  `json:"succeeded,omitempty"`
			Failed     int                  `json:"failed,omitempty"`
			Conditions []snapshot.Condition `json:"conditions,omitempty"`
		} `json:"status"`
	} `json:"items"`
}

type cronJobListJSON struct {
	Items []struct {
		Metadata metadataJSON `json:"metadata"`
		Spec     struct {
			Schedule string `json:"schedule,omitempty"`
			Suspend  bool   `json:"suspend,omitempty"`
		} `json:"spec"`
		Status struct {
			LastScheduleTime   string `json:"lastScheduleTime,omitempty"`
			LastSuccessfulTime string `json:"lastSuccessfulTime,omitempty"`
			Active             []struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace,omitempty"`
			} `json:"active,omitempty"`
		} `json:"status"`
	} `json:"items"`
}

type topListJSON struct {
	Items []struct {
		Metadata   metadataJSON `json:"metadata"`
		Containers []struct {
			Name  string `json:"name"`
			Usage struct {
				Memory string `json:"memory"`
			} `json:"usage"`
		} `json:"containers"`
	} `json:"items"`
}
