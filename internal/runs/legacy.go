package runs

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// The older delegar.sh helper writes run files with Spanish keys and status
// values to ~/.ct-delegar/estado, and its logs to ~/.ct-delegar/registros.
// panal keeps reading them; this file is the only place that knows that
// format. Nothing in panal writes it.

// legacyRun is a run file as delegar.sh writes it.
type legacyRun struct {
	Version  int    `json:"version"`
	Stamp    string `json:"sello"`
	Agent    string `json:"agente"`
	Model    string `json:"modelo"`
	Effort   string `json:"esfuerzo"`
	Task     string `json:"encargo"`
	TaskFile string `json:"encargo_archivo"`
	Dir      string `json:"carpeta"`
	ReadOnly bool   `json:"solo_leer"`
	PID      int    `json:"pid"`
	Log      string `json:"registro"`
	Start    string `json:"inicio"`
	Status   string `json:"estado"`
	End      string `json:"fin"`
	RC       *int   `json:"rc"`
}

// legacyStatus maps delegar.sh status values to the current ones.
var legacyStatus = map[string]Status{
	"corriendo":      Running,
	"termino":        Done,
	"sin_cuota":      OutOfQuota,
	"sin_permiso":    NoPermission,
	"omitido":        Skipped,
	"tiempo_agotado": Timeout,
	"fallo":          Failed,
	"interrumpido":   Interrupted,
}

func isLegacy(probe map[string]json.RawMessage) bool {
	_, hasAgente := probe["agente"]
	_, hasEstado := probe["estado"]
	return hasAgente || hasEstado
}

func parseLegacy(data []byte) (Run, error) {
	var l legacyRun
	if err := json.Unmarshal(data, &l); err != nil {
		return Run{}, err
	}
	st, ok := legacyStatus[l.Status]
	if !ok {
		st = Status(l.Status) // unknown: keep it as is
	}
	return Run{
		Version:  l.Version,
		ID:       l.Stamp,
		Agent:    l.Agent,
		Model:    l.Model,
		Effort:   l.Effort,
		Task:     l.Task,
		TaskFile: l.TaskFile,
		Dir:      l.Dir,
		ReadOnly: l.ReadOnly,
		PID:      l.PID,
		Log:      l.Log,
		Start:    l.Start,
		Status:   st,
		End:      l.End,
		RC:       l.RC,
	}, nil
}

// LegacyEnv are the variables delegar.sh uses to move its run and log
// directories; tests clear them so the machine's own setup doesn't leak in.
var LegacyEnv = []string{"DELEGAR_ESTADOS", "DELEGAR_REGISTROS"}

// legacyDir is delegar.sh's run directory: DELEGAR_ESTADOS, else
// ~/.ct-delegar/estado if it exists, else "".
func legacyDir() string { return legacyPath("DELEGAR_ESTADOS", "estado") }

// legacyLogDir is delegar.sh's log directory: DELEGAR_REGISTROS, else
// ~/.ct-delegar/registros if it exists, else "".
func legacyLogDir() string { return legacyPath("DELEGAR_REGISTROS", "registros") }

func legacyPath(env, sub string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	d := filepath.Join(userHome(), ".ct-delegar", sub)
	if fi, err := os.Stat(d); err == nil && fi.IsDir() {
		return d
	}
	return ""
}
