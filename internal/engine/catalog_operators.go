package engine

var activeCandidateEmitters = []candidateEmitter{
	emitComparison,
	emitArithmeticBinary,
	emitBitwiseBinary,
	emitUnary,
	emitCompoundAssignment,
	emitBooleanOperand,
	emitScalarValue,
	emitLoopControl,
	emitIncDec,
	emitConditions,
	emitRangeSuppression,
	emitStatementLists,
	emitAssignmentDrop,
	emitReturnSubstitution,
}
