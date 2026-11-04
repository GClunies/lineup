package scheduler

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"time"
)

// Client calls the Scheduler over its Unix socket.
type Client struct{ Path string }

func (c Client) Do(req Request, out any) error {
	conn, err := net.DialTimeout("unix", c.Path, 2*time.Second)
	if err != nil {
		return errors.New("the Scheduler is not running: run `lineup restart`")
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	if out != nil && resp.Data != nil {
		return json.Unmarshal(resp.Data, out)
	}
	return nil
}

// Watch calls f with each new Board until the connection ends.
func (c Client) Watch(f func(BoardView)) error {
	conn, err := net.DialTimeout("unix", c.Path, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(Request{Op: "watch"}); err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var v BoardView
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return err
		}
		f(v)
	}
	return sc.Err()
}
