## stampede compare

Compare two versions using repeated runs of each

### Synopsis

Compare JSON reports (from stampede run --json) of version A with version B.
Each report is a file or, when no such file exists, the id of a run on the
server you signed in to (a unique prefix of the id is enough), whose
report is downloaded.

A change counts as a regression or improvement only when the bootstrap 95%
confidence interval of the difference excludes zero and the change is larger
than the noise measured between repeats of the same version. Use at least 3
runs per version; stampede run --repeat 3 produces them.

Exit codes: 0 no regression, 4 regression, 1 error.

```
stampede compare --a <reports...> --b <reports...> [flags]
```

### Examples

```
  stampede run checkout.yaml --repeat 3 --json reports/v1.json
  stampede compare --a reports/v1-1.json,reports/v1-2.json,reports/v1-3.json --b ... --md comment.md
  stampede compare base.json head.json
  stampede compare 3f2a91c0 7be01d44
```

### Options

```
      --a strings        reports of the baseline version
      --b strings        reports of the new version
  -h, --help             help for compare
      --json string      write the comparison as JSON (- for stdout)
      --label-a string   name for the baseline (default "A")
      --label-b string   name for the new version (default "B")
      --md string        write a Markdown summary (- for stdout)
```

### SEE ALSO

* [stampede](stampede.md)	 - Self-hosted, distributed load testing

