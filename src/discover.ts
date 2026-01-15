import { createOpenAI } from "@ai-sdk/openai";
import { generateObject } from "ai";
import { DiscoverResultSchema, type DiscoverResult } from "./types";

const openrouter = createOpenAI({
  baseURL: "https://openrouter.ai/api/v1",
  apiKey: process.env.OPENROUTER_API_KEY,
});

const DISCOVER_PROMPT = `You are a log analysis expert. Analyze the following log lines and identify common patterns.

For each pattern you find:
1. Write a regex that matches logs of this type
2. Describe what this pattern represents (e.g., "User login events", "Database connection errors")

Guidelines:
- Focus on the most frequent and meaningful patterns
- Use capturing groups sparingly, prefer non-capturing groups (?:...)
- Replace variable parts (timestamps, IDs, IPs) with appropriate regex patterns
- Return 3-8 patterns, ordered by importance/frequency
- Make regexes specific enough to avoid false positives

Log lines:
`;

export async function discoverPatterns(
  logLines: string[],
  sampleSize: number = 50
): Promise<DiscoverResult> {
  const sample = logLines.slice(0, sampleSize);
  const prompt = DISCOVER_PROMPT + sample.map((l, i) => `${i + 1}. ${l}`).join("\n");

  const result = await generateObject({
    model: openrouter("google/gemini-2.0-flash-001"),
    schema: DiscoverResultSchema,
    prompt,
  });

  return result.object;
}
