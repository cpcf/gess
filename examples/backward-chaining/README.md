# Backward-chaining examples

These examples show query-driven rules that prove facts on demand instead of
materializing every possible answer up front.

- `incident_response`: proves whether one system can reach another through
  allowed network paths.
- `supply_chain_impact`: proves whether a service is affected by a vulnerable
  transitive dependency.
- `insurance_claims`: proves whether a claim is payable, including delegated
  vendor approval.

Run all examples with:

```sh
go test ./examples/backward-chaining/...
```

