package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RemoteStore struct {
	*MemoryStore
	client *http.Client
	base   string
	agent  string
	mu     sync.Mutex
	jobID  string
	target string
}

func NewRemoteStore(client *http.Client, base, agent string) *RemoteStore {
	return &RemoteStore{MemoryStore: NewMemoryStore(), client: client, base: strings.TrimRight(base, "/"), agent: agent}
}

func (r *RemoteStore) SetJob(id, target string) {
	r.mu.Lock()
	r.jobID = id
	r.target = target
	r.mu.Unlock()
}
func (r *RemoteStore) ClearJob() { r.SetJob("", "") }
func (r *RemoteStore) current() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobID, r.target
}

func (r *RemoteStore) MarkBackupResult(ctx context.Context, id string, ok bool, gid string, at time.Time) error {
	jobID, _ := r.current()
	if jobID == "" {
		return fmt.Errorf("remote store has no active job")
	}
	return agentPost(ctx, r.client, r.base+"/api/v1/agent/jobs/"+jobID+"/backup-result", map[string]any{"repository_id": id, "ok": ok, "generation_id": gid, "at": at}, nil)
}
func (r *RemoteStore) RecordGeneration(ctx context.Context, g Generation) error {
	jobID, _ := r.current()
	if jobID == "" {
		return fmt.Errorf("remote store has no active job")
	}
	if g.MetaPath != "" && !strings.HasPrefix(g.MetaPath, "agent://") {
		g.MetaPath = "agent://" + r.agent + "/" + strings.TrimLeft(strings.ReplaceAll(g.MetaPath, "\\", "/"), "/")
	}
	return agentPost(ctx, r.client, r.base+"/api/v1/agent/jobs/"+jobID+"/generation", g, nil)
}
func (r *RemoteStore) Enqueue(ctx context.Context, j Job) (Job, bool, error) {
	jobID, _ := r.current()
	if jobID == "" {
		return Job{}, false, fmt.Errorf("remote store has no active job")
	}
	var out struct {
		Job     Job  `json:"job"`
		Created bool `json:"created"`
	}
	err := agentPost(ctx, r.client, r.base+"/api/v1/agent/jobs/"+jobID+"/enqueue", j, &out)
	return out.Job, out.Created, err
}

func (r *RemoteStore) RecordReplica(ctx context.Context, rp GenerationReplica) error {
	jobID, _ := r.current()
	if jobID == "" {
		return fmt.Errorf("remote store has no active job")
	}
	if rp.MetaPath != "" && !strings.HasPrefix(rp.MetaPath, "agent://") {
		rp.MetaPath = "agent://" + r.agent + "/" + strings.TrimLeft(strings.ReplaceAll(rp.MetaPath, "\\", "/"), "/")
	}
	rp.AgentID = r.agent
	return agentPost(ctx, r.client, r.base+"/api/v1/agent/jobs/"+jobID+"/replica", rp, nil)
}

func (r *RemoteStore) UploadReplication(ctx context.Context, transferID string, src io.Reader) (installReplicaPayload, error) {
	jobID, _ := r.current()
	if jobID == "" {
		return installReplicaPayload{}, fmt.Errorf("remote store has no active job")
	}
	u := r.base + "/api/v1/agent/jobs/" + url.PathEscape(jobID) + "/replication/upload/" + url.PathEscape(transferID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, src)
	if err != nil {
		return installReplicaPayload{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := r.client.Do(req)
	if err != nil {
		return installReplicaPayload{}, err
	}
	var out installReplicaPayload
	if err := decodeJSON(resp, &out); err != nil {
		return installReplicaPayload{}, err
	}
	return out, nil
}

func (r *RemoteStore) DownloadReplication(ctx context.Context, transferID string, dst io.Writer) error {
	jobID, _ := r.current()
	if jobID == "" {
		return fmt.Errorf("remote store has no active job")
	}
	u := r.base + "/api/v1/agent/jobs/" + url.PathEscape(jobID) + "/replication/download/" + url.PathEscape(transferID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("agent API %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

func (r *RemoteStore) AdjustObjectRef(ctx context.Context, ref ObjectRef, delta int64) (ObjectRef, error) {
	jobID, _ := r.current()
	if jobID == "" {
		return ObjectRef{}, fmt.Errorf("remote store has no active job")
	}
	var out ObjectRef
	err := agentPost(ctx, r.client, r.base+"/api/v1/agent/jobs/"+url.PathEscape(jobID)+"/object-ref", agentObjectRefRequest{Ref: ref, Delta: delta}, &out)
	return out, err
}
func (r *RemoteStore) EnsureObjectRef(ctx context.Context, ref ObjectRef, owner string) (bool, error) {
	jobID, _ := r.current()
	if jobID == "" {
		return false, fmt.Errorf("remote store has no active job")
	}
	var out struct {
		Created bool `json:"created"`
	}
	err := agentPost(ctx, r.client, r.base+"/api/v1/agent/jobs/"+url.PathEscape(jobID)+"/object-ref", agentObjectRefRequest{Ref: ref, Owner: owner, Ensure: true}, &out)
	return out.Created, err
}

func (r *RemoteStore) RecordErasureSet(ctx context.Context, e ErasureSet) error {
	jobID, _ := r.current()
	if jobID == "" {
		return fmt.Errorf("remote store has no active job")
	}
	return agentPost(ctx, r.client, r.base+"/api/v1/agent/jobs/"+url.PathEscape(jobID)+"/erasure-set", e, nil)
}
func (r *RemoteStore) RecordErasureShard(ctx context.Context, sh ErasureShard) error {
	jobID, _ := r.current()
	if jobID == "" {
		return fmt.Errorf("remote store has no active job")
	}
	sh.AgentID = r.agent
	return agentPost(ctx, r.client, r.base+"/api/v1/agent/jobs/"+url.PathEscape(jobID)+"/erasure-shard", sh, nil)
}

func (r *RemoteStore) ReportCorruptObject(ctx context.Context, digest string) error {
	jobID, _ := r.current()
	if jobID == "" {
		return fmt.Errorf("remote store has no active job")
	}
	return agentPost(ctx, r.client, r.base+"/api/v1/agent/jobs/"+url.PathEscape(jobID)+"/corrupt-object", map[string]string{"digest": digest}, nil)
}

var _ Store = (*RemoteStore)(nil)

// UploadReplicationFile performs offset-checked chunked upload. Every chunk is
// independently SHA-256 authenticated; after a transient failure the client
// re-queries the authoritative server offset and resumes without retransmitting
// already fsynced bytes.
func (r *RemoteStore) UploadReplicationFile(ctx context.Context, transferID string, src *os.File, chunkBytes int64, retries int, bandwidthMbps int) (installReplicaPayload, error) {
	jobID, _ := r.current()
	if jobID == "" {
		return installReplicaPayload{}, fmt.Errorf("remote store has no active job")
	}
	if chunkBytes <= 0 {
		chunkBytes = 8 << 20
	}
	if retries < 1 {
		retries = 3
	}
	base := r.base + "/api/v1/agent/jobs/" + url.PathEscape(jobID) + "/replication/upload/" + url.PathEscape(transferID)
	info, err := src.Stat()
	if err != nil {
		return installReplicaPayload{}, err
	}
	fullHash, err := hashOpenFile(src)
	if err != nil {
		return installReplicaPayload{}, err
	}
	offset, state, err := r.replicationUploadOffset(ctx, base)
	if err != nil {
		offset = 0
	}
	if state == TransferReady && offset == info.Size() {
		// Idempotent retry after finalize: ask finalize again; server-side state
		// and active-job deduplication make this safe.
		offset = info.Size()
	}
	buf := make([]byte, chunkBytes)
	for offset < info.Size() {
		if _, err := src.Seek(offset, io.SeekStart); err != nil {
			return installReplicaPayload{}, err
		}
		want := int64(len(buf))
		if remain := info.Size() - offset; remain < want {
			want = remain
		}
		n, err := io.ReadFull(src, buf[:want])
		if err != nil && err != io.ErrUnexpectedEOF {
			return installReplicaPayload{}, err
		}
		chunk := buf[:n]
		sum := sha256.Sum256(chunk)
		var last error
		chunkStarted := time.Now()
		for attempt := 0; attempt < retries; attempt++ {
			req, e := http.NewRequestWithContext(ctx, http.MethodPatch, base, bytes.NewReader(chunk))
			if e != nil {
				return installReplicaPayload{}, e
			}
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("X-RepoArk-Offset", strconv.FormatInt(offset, 10))
			req.Header.Set("X-RepoArk-Chunk-SHA256", hex.EncodeToString(sum[:]))
			resp, e := r.client.Do(req)
			if e == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				_ = resp.Body.Close()
				offset += int64(n)
				last = nil
				paceBytes(ctx, chunkStarted, int64(n), bandwidthMbps)
				break
			}
			if resp != nil {
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
				_ = resp.Body.Close()
				if e == nil {
					e = fmt.Errorf("chunk upload %s: %s", resp.Status, strings.TrimSpace(string(b)))
				}
			}
			last = e
			serverOffset, _, se := r.replicationUploadOffset(ctx, base)
			if se == nil && serverOffset != offset {
				offset = serverOffset
				last = nil
				break
			}
			select {
			case <-ctx.Done():
				return installReplicaPayload{}, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
			}
		}
		if last != nil {
			return installReplicaPayload{}, last
		}
	}
	body, _ := json.Marshal(replicationFinalizeRequest{SHA256: fullHash, Bytes: info.Size()})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/finalize", bytes.NewReader(body))
	if err != nil {
		return installReplicaPayload{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return installReplicaPayload{}, err
	}
	var out installReplicaPayload
	if err := decodeJSON(resp, &out); err != nil {
		return installReplicaPayload{}, err
	}
	return out, nil
}

func (r *RemoteStore) replicationUploadOffset(ctx context.Context, u string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, "", fmt.Errorf("upload status %s", resp.Status)
	}
	offset, err := strconv.ParseInt(resp.Header.Get("X-RepoArk-Offset"), 10, 64)
	if err != nil {
		return 0, "", err
	}
	return offset, resp.Header.Get("X-RepoArk-State"), nil
}

func hashOpenFile(f *os.File) (string, error) {
	pos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	_, _ = f.Seek(pos, io.SeekStart)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DownloadReplicationFile resumes a Range-capable relay download into a
// deterministic partial file and verifies the final ciphertext hash.
func (r *RemoteStore) DownloadReplicationFile(ctx context.Context, transferID, path string, expectedBytes int64, expectedSHA string, retries int, bandwidthMbps int) error {
	jobID, _ := r.current()
	if jobID == "" {
		return fmt.Errorf("remote store has no active job")
	}
	if retries < 1 {
		retries = 3
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	u := r.base + "/api/v1/agent/jobs/" + url.PathEscape(jobID) + "/replication/download/" + url.PathEscape(transferID)
	for attempt := 0; attempt < retries; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return err
		}
		offset := info.Size()
		if expectedBytes > 0 && offset == expectedBytes {
			_ = f.Close()
			got, n, he := hashFileAtPath(path)
			if he == nil && n == expectedBytes && (expectedSHA == "" || strings.EqualFold(got, expectedSHA)) {
				return nil
			}
			f, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			offset = 0
		}
		if expectedBytes > 0 && offset > expectedBytes {
			_ = f.Truncate(0)
			offset = 0
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = f.Close()
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			_ = f.Close()
			return err
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		resp, err := r.client.Do(req)
		if err == nil && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
			if offset > 0 && resp.StatusCode == http.StatusOK {
				_ = f.Truncate(0)
				_, _ = f.Seek(0, io.SeekStart)
				offset = 0
			}
			_, cpErr := copyPaced(ctx, f, resp.Body, bandwidthMbps)
			_ = resp.Body.Close()
			syncErr := f.Sync()
			_ = f.Close()
			if cpErr == nil {
				cpErr = syncErr
			}
			if cpErr == nil {
				got, n, he := hashFileAtPath(path)
				if he == nil && (expectedBytes <= 0 || n == expectedBytes) && (expectedSHA == "" || strings.EqualFold(got, expectedSHA)) {
					return nil
				}
				if he == nil {
					cpErr = fmt.Errorf("downloaded replication checksum/size mismatch")
				} else {
					cpErr = he
				}
			}
			err = cpErr
		} else {
			if resp != nil {
				_ = resp.Body.Close()
			}
			_ = f.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 250 * time.Millisecond):
		}
	}
	return fmt.Errorf("resumable replication download failed")
}

func paceBytes(ctx context.Context, started time.Time, n int64, mbps int) {
	if mbps <= 0 || n <= 0 {
		return
	}
	want := time.Duration(float64(n*8) / (float64(mbps) * 1_000_000) * float64(time.Second))
	if remain := want - time.Since(started); remain > 0 {
		t := time.NewTimer(remain)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		}
	}
}

func copyPaced(ctx context.Context, dst io.Writer, src io.Reader, mbps int) (int64, error) {
	if mbps <= 0 {
		return io.Copy(dst, src)
	}
	buf := make([]byte, 256<<10)
	var total int64
	for {
		started := time.Now()
		n, er := src.Read(buf)
		if n > 0 {
			wn, ew := dst.Write(buf[:n])
			total += int64(wn)
			if ew != nil {
				return total, ew
			}
			if wn != n {
				return total, io.ErrShortWrite
			}
			paceBytes(ctx, started, int64(n), mbps)
		}
		if er == io.EOF {
			return total, nil
		}
		if er != nil {
			return total, er
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

func hashFileAtPath(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
