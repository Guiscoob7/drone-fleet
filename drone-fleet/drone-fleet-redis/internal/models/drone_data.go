// internal/models/drone_data.go

package models

import "encoding/json"

// DroneStatus representa o estado atual do Drone
type DroneStatus struct {
	ID        int     `json:"id"`
	Battery   int     `json:"battery"`
	PositionX float64 `json:"position_x"`
	PositionY float64 `json:"position_y"`
	State     string  `json:"state"` // "IDLE", "BUSY", "CHARGING", "LOW_BATTERY", "EMERGENCY"
	TaskID    string  `json:"task_id,omitempty"`
}

func (d *DroneStatus) ToJSON() ([]byte, error) {
	return json.Marshal(d)
}

func FromJSON(data []byte) (*DroneStatus, error) {
	var drone DroneStatus
	err := json.Unmarshal(data, &drone)
	return &drone, err
}

func (d *DroneStatus) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"id":         d.ID,
		"battery":    d.Battery,
		"position_x": d.PositionX,
		"position_y": d.PositionY,
		"state":      d.State,
		"task_id":    d.TaskID,
	}
}
