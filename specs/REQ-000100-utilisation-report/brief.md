# Business brief: Pool utilisation reporting

**Requirement**: REQ-000100 (appvia/ipam#100)
**Created**: 2026-11-19
**Status**: Draft

## Problem

Network administrators at the fictional Example Org cannot see how address space in their IPAM pools is used over time. Today they inspect pools individually and copy figures into a spreadsheet each month, taking about half a day and producing stale results.

## Outcome

- **OM-1**: An administrator can produce an on-demand utilisation report across all pools with pool name, total addresses, allocated addresses, free addresses and percent used in under one minute.
- **OM-2**: An administrator can look back at earlier reports and compare utilisation month on month.

## Users

- **Network administrators**: Need a current, complete report across all pools.
- **Capacity planners**: Need historical utilisation to identify month-on-month changes.

## Constraints

- Use only data already stored about pools and allocations; do not add an external system.
- Reporting must not change allocation behaviour or stored allocation state.
- Pool utilisation must be calculated from the existing pool and allocation documents.
- Any historical view must preserve the existing storage backend boundary and add no external service.

## Assumptions

- **ASM-1**: Percent used is `allocated / total * 100`, rounded to one decimal place. This gives a consistent, readable measure across the report and historical comparisons.
- **ASM-2**: The report covers every usable pool in the configured backend and fails clearly if pool data cannot be read rather than showing a misleading partial report.

## Out of scope

- Alerting or notifications for utilisation thresholds.
- Forecasting future address usage.
- Changing pool allocation strategy or allocation behaviour.
- Any external reporting or data-warehouse system.

## Open questions

- [NEEDS CLARIFICATION: Where should administrators run the on-demand report? - Recommended: Add an `ipam` CLI report command with machine-readable JSON output - It reuses the existing product interface and keeps the report usable by administrators and automation.]
- [NEEDS CLARIFICATION: How should earlier reports be retained and recreated? - Recommended: Persist a monthly utilisation snapshot in the existing storage backend and retain the most recent 13 monthly snapshots - The 13-month window supports 12 month-to-month comparisons plus the current month while adding no external system.]
