import { discoverPatterns } from "./discover";
import { applyPipeline, printPipelineResult } from "./pipeline";
import type { Pattern } from "./types";

async function readStdin(): Promise<string[]> {
  const chunks: Buffer[] = [];

  for await (const chunk of Bun.stdin.stream()) {
    chunks.push(chunk);
  }

  const text = Buffer.concat(chunks).toString("utf-8");
  return text.split("\n").filter((line) => line.trim() !== "");
}

async function main() {
  const args = process.argv.slice(2);
  const command = args[0] || "discover";

  console.error("Reading logs from stdin...");
  const logLines = await readStdin();
  console.error(`Read ${logLines.length} lines\n`);

  if (logLines.length === 0) {
    console.error("No input received. Usage: cat logs.txt | bun run src/index.ts");
    process.exit(1);
  }

  if (command === "discover") {
    console.error("Discovering patterns...\n");
    const result = await discoverPatterns(logLines);

    console.log("=== Discovered Patterns ===\n");
    result.patterns.forEach((p, i) => {
      console.log(`${i + 1}. ${p.description}`);
      console.log(`   Regex: ${p.regex}\n`);
    });

    console.log("\n=== Applying Pipeline ===");
    const pipelineResult = applyPipeline(logLines, result.patterns);
    printPipelineResult(pipelineResult);

    console.log("\n=== Patterns JSON (for saving) ===\n");
    console.log(JSON.stringify(result.patterns, null, 2));
  } else if (command === "apply") {
    const patternsFile = args[1];
    if (!patternsFile) {
      console.error("Usage: cat logs.txt | bun run src/index.ts apply patterns.json");
      process.exit(1);
    }

    const patternsJson = await Bun.file(patternsFile).text();
    const patterns: Pattern[] = JSON.parse(patternsJson);

    console.error(`Applying ${patterns.length} patterns...\n`);
    const result = applyPipeline(logLines, patterns);
    printPipelineResult(result);
  } else {
    console.error(`Unknown command: ${command}`);
    console.error("Commands: discover, apply");
    process.exit(1);
  }
}

main().catch((e) => {
  console.error("Error:", e);
  process.exit(1);
});
