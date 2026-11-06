package git

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Blobs streams object contents through one long-lived `git cat-file --batch`.
type Blobs struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

// OpenBlobs starts git cat-file in the repository at dir; Close ends it.
func (g *Client) OpenBlobs(ctx context.Context, dir string) (*Blobs, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "cat-file", "--batch")
	cmd.Env = g.env(ctx, Remote{})
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Blobs{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<16)}, nil
}

// Read returns the content of blob sha, or an error when sha names no blob.
func (b *Blobs) Read(sha string) ([]byte, error) {
	if _, err := io.WriteString(b.in, sha+"\n"); err != nil {
		return nil, err
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return nil, err
	}
	f := strings.Fields(header) // <sha> blob <size> | <sha> missing
	if len(f) != 3 || f[1] != "blob" {
		return nil, fmt.Errorf("git cat-file %s: %s", sha, strings.TrimSpace(header))
	}
	size, err := strconv.Atoi(f[2])
	if err != nil {
		return nil, err
	}
	buf := make([]byte, size+1) // content plus the trailing newline
	if _, err := io.ReadFull(b.out, buf); err != nil {
		return nil, err
	}
	return buf[:size], nil
}

// Close lets git finish. A Read that failed on an object that is not a blob
// leaves that object's body in the pipe, and git cannot exit before it is read.
func (b *Blobs) Close() error {
	b.in.Close()
	io.Copy(io.Discard, b.out)
	return b.cmd.Wait()
}
