"""Writes a markdown report of run.py's output: medians, change against the reference, Mann-Whitney U p."""
import csv
import statistics
import sys

from scipy.stats import mannwhitneyu

METRICS = [("wall_s", "Wall time", "s"), ("gazelle_cpu_s", "Gazelle CPU", "s"), ("total_cpu_s", "Total CPU", "s"),
           ("jvm_maxrss_mb", "JVM peak RSS", "MB"), ("gazelle_maxrss_mb", "Gazelle peak RSS", "MB")]


def main(out_dir, reference, labels):
    labels = dict(x.split("=", 1) for x in labels.split(","))
    runs = [r for r in csv.DictReader(open(f"{out_dir}/runs.csv")) if int(r["round"]) >= 0]
    bad = [r for r in runs if r["rc"] != "0"]
    identity = list(csv.DictReader(open(f"{out_dir}/identity.csv")))
    print("## Output identity\n")
    print("| Repo | Config | Exit code | BUILD files | `diff -r` lines vs " + reference + " |")
    print("|---|---|---|---|---|")
    for r in identity:
        print(f"| {r['repo']} | {labels.get(r['config'], r['config'])} | {r['rc']} | {r['build_files']} | {r['diff_lines'] or '-'} |")
    print()
    if bad:
        print(f"**{len(bad)} timed runs exited non-zero.**\n")
    configs = list(dict.fromkeys(r["config"] for r in runs))
    for repo in dict.fromkeys(r["repo"] for r in runs):
        base = [r for r in runs if r["repo"] == repo and r["config"] == reference]
        n = len(base)
        print(f"## {repo} (median of {n} runs; p = two-sided Mann-Whitney U against {labels.get(reference, reference)})\n")
        for c in configs:
            if c == reference:
                continue
            xs = [r for r in runs if r["repo"] == repo and r["config"] == c]
            print(f"**{labels.get(c, c)}**\n")
            print(f"| Metric | {labels.get(reference, reference)} | {labels.get(c, c)} | Change | p |")
            print("|---|---|---|---|---|")
            for key, name, unit in METRICS:
                a, b = [float(r[key]) for r in base], [float(r[key]) for r in xs]
                ma, mb = statistics.median(a), statistics.median(b)
                p = mannwhitneyu(b, a, alternative="two-sided").pvalue
                print(f"| {name} | {ma:.2f} {unit} | {mb:.2f} {unit} | {(mb / ma - 1) * 100:+.0f}% | {p:.2g} |")
            print()


if __name__ == "__main__":
    main(*sys.argv[1:])
