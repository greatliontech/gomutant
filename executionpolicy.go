package gomutant

// FullOracleExecutionPolicy identifies measurements made without
// coverage-based negative exemptions or cross-oracle drift composition, with
// Go-compatible test inventory, admitted skip settings and named baseline passes.
// Changed oracle evidence requires whole-target remeasurement. Exact reuse,
// unchanged-input prefix extension and candidate-local re-execution retain
// their independent evidence requirements.
const FullOracleExecutionPolicy = "gomutant/full-oracle@3"

// OracleExecutionPolicyIssue is the measurement-policy admission shared by
// reuse, inspection and new survivor dispositions. Earlier body and shaped
// records lack complete oracle-admission and named-baseline authority. Their
// unsplit patterns alone cannot establish which tests actually ran or passed.
// An empty answer means this policy permits further evidence judgment,
// never that the record is currently reusable.
func OracleExecutionPolicyIssue(f Finding) string {
	if f.OracleExecutionPolicy == FullOracleExecutionPolicy {
		return ""
	}
	return "oracle execution policy is missing or unsupported; re-measure with the complete oracle"
}
