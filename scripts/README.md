# Scripts

## Local GA Baseline Verification

`verify-ga-baseline.sh` is the local GA implementation baseline verification
entrypoint for this repo. It runs the current local test and contract gates:

```bash
bash scripts/verify-ga-baseline.sh
```

Passing this script means the local implementation baseline checks passed. It
does not mean final production GA has been accepted or completed.

Future scripts may include local dev setup, smoke tests, contract generation,
and release checks.
