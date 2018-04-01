package alternatives // import "github.com/daaku/debdiff/alternatives"

import (
	"bufio"
	"bytes"
	"os/exec"

	"github.com/pkg/errors"
)

// GetSelections list master alternative names and their status.
func GetSelections() ([]string, error) {
	out, err := exec.Command(
		"update-alternatives", "--get-selections").CombinedOutput()
	if err != nil {
		return nil, errors.Wrap(err, "error getting selections")
	}

	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	res := make([]string, 0, len(lines))
	for _, line := range lines {
		res = append(res, string(line[:bytes.IndexRune(line, ' ')]))
	}
	return res, nil
}

// QueryResultAlternative is an alternative in the QueryResult.
type QueryResultAlternative struct {
	Alternative string
	Priority    string
	Slaves      map[string]string
}

// QueryResult contains information about a named group.
type QueryResult struct {
	Name         string
	Link         string
	Slaves       map[string]string
	Status       string
	Best         string
	Value        string
	Alternatives []QueryResultAlternative
}

// Query information about a named group.
func Query(name string) (QueryResult, error) {
	out, err := exec.Command(
		"update-alternatives", "--query", name).CombinedOutput()
	if err != nil {
		return QueryResult{}, errors.Wrapf(err, "error querying for %q", name)
	}

	var qr QueryResult
	sc := bufio.NewScanner(bytes.NewReader(out))
	if err := parseQueryResult(sc, &qr); err != nil {
		return QueryResult{}, errors.Wrapf(err,
			"error parsing query result for %q", name)
	}
	if err := sc.Err(); err != nil {
		return QueryResult{}, errors.Wrapf(err,
			"error parsing query result for %q", name)
	}

	return qr, nil
}

var (
	prefixName        = []byte("Name: ")
	prefixLink        = []byte("Link: ")
	prefixStatus      = []byte("Status: ")
	prefixBest        = []byte("Best: ")
	prefixValue       = []byte("Value: ")
	prefixAlternative = []byte("Alternative: ")
	prefixPriority    = []byte("Priority: ")
	exactSlaves       = []byte("Slaves:")
)

func parseQueryResult(sc *bufio.Scanner, qr *QueryResult) error {
	for sc.Scan() {
		data := sc.Bytes()

		if bytes.Equal(data, exactSlaves) {
			if err := parseSlaves(sc, &qr.Slaves, &data); err != nil {
				return err
			}
		}

		switch {
		default:
			return errors.Errorf("error parsing query result: %q", data)
		case len(data) == 0:
			return parseQueryResultAlternatives(sc, qr)
		case bytes.HasPrefix(data, prefixName):
			qr.Name = string(data[len(prefixName):])
		case bytes.HasPrefix(data, prefixLink):
			qr.Link = string(data[len(prefixLink):])
		case bytes.HasPrefix(data, prefixStatus):
			qr.Status = string(data[len(prefixStatus):])
		case bytes.HasPrefix(data, prefixBest):
			qr.Best = string(data[len(prefixBest):])
		case bytes.HasPrefix(data, prefixValue):
			qr.Value = string(data[len(prefixValue):])
		}
	}
	return nil
}

func parseQueryResultAlternatives(sc *bufio.Scanner, qr *QueryResult) error {
	var alt QueryResultAlternative
	for sc.Scan() {
		data := sc.Bytes()

		if bytes.Equal(data, exactSlaves) {
			if err := parseSlaves(sc, &alt.Slaves, &data); err != nil {
				return err
			}
		}

		switch {
		default:
			return errors.Errorf("error parsing query alternative: %q", data)
		case len(data) == 0:
			qr.Alternatives = append(qr.Alternatives, alt)
			alt = QueryResultAlternative{}
			continue
		case bytes.HasPrefix(data, prefixAlternative):
			alt.Alternative = string(data[len(prefixAlternative):])
		case bytes.HasPrefix(data, prefixPriority):
			alt.Priority = string(data[len(prefixPriority):])
		}
	}
	return nil
}

func parseSlaves(
	sc *bufio.Scanner,
	target *map[string]string,
	pending *[]byte,
) error {
	slaves := *target
	if slaves == nil {
		slaves = make(map[string]string)
		*target = slaves
	}
	for sc.Scan() {
		data := sc.Bytes()
		if bytes.HasPrefix(data, []byte(" ")) {
			res := bytes.SplitN(data[1:], []byte(" "), 2)
			slaves[string(res[0])] = string(res[1])
			continue
		}
		*pending = data
		return nil
	}
	// scanner ended, nothing pending
	*pending = nil
	return nil
}
