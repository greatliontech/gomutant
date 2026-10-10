package gomutant

// FullOracleExecutionPolicy identifies measurements made without
// coverage-based negative exemptions or cross-oracle drift composition.
// Changed oracle evidence requires whole-target remeasurement. Exact reuse,
// unchanged-input prefix extension and candidate-local re-execution retain
// their independent evidence requirements.
const FullOracleExecutionPolicy = "gomutant/full-oracle@2"

// unsplitShapedPolicy identifies the earlier unsplit execution policy.
// Shaped measurements never entered the body-only oracle-drift path.
const unsplitShapedPolicy = "gomutant/full-oracle@1"

// OracleExecutionPolicyIssue is the measurement-policy admission shared by
// reuse, inspection and new survivor dispositions. Historical shaped records
// ran unsplit by construction and never used body-only drift composition.
// Policy-1 body records do not distinguish a fresh full execution from an
// unsound composition; an apparently unchanged current record can therefore
// already carry contaminated outcomes.
// An empty answer means this policy permits further evidence judgment,
// never that the record is currently reusable.
func OracleExecutionPolicyIssue(f Finding) string {
	if f.OracleExecutionPolicy == FullOracleExecutionPolicy || f.Shape != nil && (f.OracleExecutionPolicy == "" || f.OracleExecutionPolicy == unsplitShapedPolicy) {
		return ""
	}
	return "oracle execution policy is missing or unsupported; re-measure with the complete oracle"
}
