"""Times gazelle binaries on prepared Java repos, interleaved, and checks their output is identical.

usage: run.py --bin-dir DIR --repo NAME=DIR ... --configs main,alloc,... --rounds N --out DIR

Each binary is bin-dir/gazelle-<config>. Gazelle and the javaparser JVM are timed separately: the JVM
is started through a wrapper that runs it under GNU time in the background, so its usage is not
counted in gazelle's.
"""
import argparse
import csv
import os
import re
import shutil
import subprocess
import time

BUILD_NAMES = ("BUILD", "BUILD.bazel")


def gnu_time(path):
    text = open(path).read()

    def field(name):
        return re.search(re.escape(name) + r": (\S+)", text).group(1)

    return {"user_s": float(field("User time (seconds)")), "sys_s": float(field("System time (seconds)")),
            "maxrss_mb": int(field("Maximum resident set size (kbytes)")) / 1024}


def write_java_wrapper(directory, real_java_home):
    os.makedirs(os.path.join(directory, "bin"), exist_ok=True)
    path = os.path.join(directory, "bin", "java")
    with open(path, "w") as f:
        f.write('#!/bin/sh\n/usr/bin/time -v -o "$JVM_TIME_FILE" "%s/bin/java" "$@" &\n' % real_java_home)
    os.chmod(path, 0o755)


def build_files(root):
    for d, dirs, files in os.walk(root):
        dirs[:] = [x for x in dirs if x != ".git"]
        for name in files:
            if name in BUILD_NAMES:
                yield os.path.join(d, name)


def reset(pristine, work):
    if not os.path.isdir(work):
        shutil.copytree(pristine, work, symlinks=True)
    for p in list(build_files(work)):
        os.remove(p)
    for p in build_files(pristine):
        shutil.copy2(p, os.path.join(work, os.path.relpath(p, pristine)))


def run_one(args, config, pristine, work, tmp):
    reset(pristine, work)
    gz_time, jvm_time = os.path.join(tmp, "gazelle.time"), os.path.join(tmp, "jvm.time")
    for p in (gz_time, jvm_time):
        if os.path.exists(p):
            os.remove(p)
    env = dict(os.environ, GAZELLE_JAVA_JAVAHOME=args.java_wrapper, JVM_TIME_FILE=jvm_time)
    cmd = ["/usr/bin/time", "-v", "-o", gz_time, os.path.join(args.bin_dir, "gazelle-" + config),
           "update", "-repo_root=" + work, "-lang=java"]
    start = time.perf_counter()
    log = os.path.join(tmp, "gazelle.log")
    with open(log, "w") as out:
        rc = subprocess.run(cmd, env=env, stdout=out, stderr=out, cwd=work).returncode
    wall = time.perf_counter() - start
    deadline = time.time() + 60
    while time.time() < deadline and not (os.path.exists(jvm_time) and os.path.getsize(jvm_time) > 0):
        time.sleep(0.2)
    time.sleep(0.5)
    if rc != 0 or not os.path.exists(jvm_time):
        print(open(log).read()[-4000:], flush=True)
        raise SystemExit(f"gazelle-{config} failed on {work}: rc={rc}, jvm timing written={os.path.exists(jvm_time)}")
    gz, jvm = gnu_time(gz_time), gnu_time(jvm_time)
    return {"rc": rc, "wall_s": round(wall, 3),
            "gazelle_cpu_s": round(gz["user_s"] + gz["sys_s"], 3), "jvm_cpu_s": round(jvm["user_s"] + jvm["sys_s"], 3),
            "total_cpu_s": round(gz["user_s"] + gz["sys_s"] + jvm["user_s"] + jvm["sys_s"], 3),
            "gazelle_maxrss_mb": round(gz["maxrss_mb"], 1), "jvm_maxrss_mb": round(jvm["maxrss_mb"], 1)}


def save_builds(work, dest):
    shutil.rmtree(dest, ignore_errors=True)
    n = 0
    for p in build_files(work):
        out = os.path.join(dest, os.path.relpath(p, work))
        os.makedirs(os.path.dirname(out), exist_ok=True)
        shutil.copy2(p, out)
        n += 1
    return n


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bin-dir", required=True)
    ap.add_argument("--repo", action="append", required=True, help="NAME=PRISTINE_DIR")
    ap.add_argument("--configs", required=True)
    ap.add_argument("--rounds", type=int, default=10)
    ap.add_argument("--java-home", default=os.environ.get("JAVA_HOME"))
    ap.add_argument("--out", required=True)
    args = ap.parse_args()
    args.bin_dir, args.out = os.path.abspath(args.bin_dir), os.path.abspath(args.out)
    configs = args.configs.split(",")
    os.makedirs(args.out, exist_ok=True)
    tmp = os.path.join(args.out, "tmp")
    os.makedirs(tmp, exist_ok=True)
    args.java_wrapper = os.path.join(tmp, "java-home")
    write_java_wrapper(args.java_wrapper, args.java_home)
    repos = {k: os.path.abspath(v) for k, v in (r.split("=", 1) for r in args.repo)}

    identity = []
    for name, pristine in repos.items():
        work = os.path.join(tmp, "work-" + name)
        for c in configs:
            row = run_one(args, c, pristine, work, tmp)
            n = save_builds(work, os.path.join(tmp, "identity", name, c))
            if c == configs[0]:
                identity.append({"repo": name, "config": c, "rc": row["rc"], "build_files": n, "diff_lines": ""})
                continue
            d = subprocess.run(["diff", "-r", os.path.join(tmp, "identity", name, configs[0]),
                                os.path.join(tmp, "identity", name, c)], capture_output=True, text=True)
            identity.append({"repo": name, "config": c, "rc": row["rc"], "build_files": n,
                             "diff_lines": len(d.stdout.splitlines())})
            print(f"identity {name} {c}: rc={row['rc']} files={n} diff_lines={identity[-1]['diff_lines']}", flush=True)
    with open(os.path.join(args.out, "identity.csv"), "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(identity[0]))
        w.writeheader()
        w.writerows(identity)

    fields = ["repo", "config", "round", "rc", "wall_s", "gazelle_cpu_s", "jvm_cpu_s", "total_cpu_s",
              "gazelle_maxrss_mb", "jvm_maxrss_mb"]
    with open(os.path.join(args.out, "runs.csv"), "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=fields)
        w.writeheader()
        for rnd in range(-1, args.rounds):  # round -1 is a warm-up, not analysed
            for name, pristine in repos.items():
                order = configs if rnd % 2 == 0 else list(reversed(configs))
                for c in order:
                    row = run_one(args, c, pristine, os.path.join(tmp, "work-" + name), tmp)
                    row.update(repo=name, config=c, round=rnd)
                    w.writerow(row)
                    f.flush()
                    print(f"round {rnd} {name} {c}: wall={row['wall_s']} gz_cpu={row['gazelle_cpu_s']} "
                          f"jvm_cpu={row['jvm_cpu_s']} jvm_rss={row['jvm_maxrss_mb']} rc={row['rc']}", flush=True)


if __name__ == "__main__":
    main()
