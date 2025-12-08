package loadtest

import (
	"encoding/json"
	"io"
)

func SaveMetricsJSON(m *Metrics, w io.Writer) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}

	_, err = w.Write(data)
	return err
}
