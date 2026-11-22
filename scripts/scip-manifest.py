"""Print the manifest deepcode import-scip reads for a scip-go index.

Run it, kept outside the checkout, in the Go module root of a clean checkout of the snapshot's commit:
    python3 /path/to/scip-manifest.py <repo-id> <index.scip> > manifest.json
<repo-id> is the id in deepcode's /repos/<id> URL.
"""
import hashlib, json, pathlib, subprocess, sys


def git(*args):
    return subprocess.check_output(["git", *args])


commit = git("rev-parse", "HEAD").decode().strip()
module_root = git("rev-parse", "--show-prefix").decode().removesuffix("\n").removesuffix("/")
prefix = (module_root + "/").encode() if module_root else b""
files = {}
for entry in git("ls-tree", "--full-tree", "-rz", commit).split(b"\0"):
    if not entry:
        continue
    header, path = entry.split(b"\t", 1)
    mode, _, oid = header.split()
    if mode in (b"100644", b"100755") and path.endswith(b".go") and path.startswith(prefix):
        files[path.decode()] = oid.decode()
print(json.dumps({
    "repository": sys.argv[1],
    "commit": commit,
    "module_root": module_root,
    "index_sha256": hashlib.sha256(pathlib.Path(sys.argv[2]).read_bytes()).hexdigest(),
    "files": files,
}))
