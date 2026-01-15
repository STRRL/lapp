import type { Pattern, PipelineResult } from "./types";

export function applyPipeline(
  logLines: string[],
  patterns: Pattern[]
): PipelineResult {
  const streams = new Map<string, string[]>();
  const leftover: string[] = [];

  for (const pattern of patterns) {
    streams.set(pattern.description, []);
  }

  for (const line of logLines) {
    let matched = false;

    for (const pattern of patterns) {
      try {
        const regex = new RegExp(pattern.regex);
        if (regex.test(line)) {
          streams.get(pattern.description)!.push(line);
          matched = true;
          break;
        }
      } catch (e) {
        console.error(`Invalid regex: ${pattern.regex}`, e);
      }
    }

    if (!matched) {
      leftover.push(line);
    }
  }

  return { streams, leftover };
}

export function printPipelineResult(result: PipelineResult): void {
  console.log("\n=== Pipeline Result ===\n");

  for (const [description, lines] of result.streams) {
    console.log(`[${description}] - ${lines.length} lines`);
  }

  console.log(`\n[leftover] - ${result.leftover.length} lines`);

  const total =
    Array.from(result.streams.values()).reduce((acc, lines) => acc + lines.length, 0) +
    result.leftover.length;

  console.log(`\nTotal: ${total} lines`);

  if (result.leftover.length > 0) {
    console.log("\n=== Leftover Sample (first 10) ===\n");
    result.leftover.slice(0, 10).forEach((line) => console.log(line));
  }
}
