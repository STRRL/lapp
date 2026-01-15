import { z } from "zod";

export const PatternSchema = z.object({
  regex: z.string().describe("Regular expression to match this log pattern"),
  description: z.string().describe("What this pattern represents"),
});

export const DiscoverResultSchema = z.object({
  patterns: z.array(PatternSchema).describe("Discovered log patterns"),
});

export type Pattern = z.infer<typeof PatternSchema>;
export type DiscoverResult = z.infer<typeof DiscoverResultSchema>;

export interface PipelineResult {
  streams: Map<string, string[]>;
  leftover: string[];
}
