package gomutant

// FullOracleExecutionPolicy identifies measurements made without
// coverage-based negative exemptions. A survivor stands on every required
// oracle group; named-killer confirmation and explicitly licensed reuse of
// prior passing evidence retain their own attribution/composition rules.
const FullOracleExecutionPolicy = "gomutant/full-oracle@1"

// OracleExecutionPolicyIssue is the measurement-policy admission shared by
// reuse, inspection and new survivor dispositions. Historical shaped records
// ran unsplit by construction; a body record's lack of a narrowed bucket
// does not prove that every candidate ran without negative exemptions.
// An empty answer means this policy permits further evidence judgment,
// never that the record is currently reusable.
func OracleExecutionPolicyIssue(f Finding) string {
	if f.OracleExecutionPolicy == FullOracleExecutionPolicy || f.OracleExecutionPolicy == "" && f.Shape != nil {
		return ""
	}
	return "oracle execution policy is missing or unsupported; re-measure with the complete oracle"
}
