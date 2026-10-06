/** Generation pipeline stages, in order (internal/ai/pipeline.go). */
export const stages = ['understand', 'draft', 'static-check', 'dry-run', 'repair', 'done'] as const;

export const stageLabels: Record<string, string> = {
  understand: 'Understand',
  draft: 'Draft',
  'static-check': 'Static check',
  'dry-run': 'Dry run',
  repair: 'Repair',
  done: 'Done',
};

export const stageHelp: Record<string, string> = {
  understand: 'Reading the inputs and mapping which calls feed values to others.',
  draft: 'The model is writing the scenario.',
  'static-check': 'Checking that the draft parses, compiles and only calls known endpoints.',
  'dry-run': 'Running each journey once with one user against the target.',
  repair: 'Sending the problems and redacted traces back to the model.',
  done: 'Finished.',
};
