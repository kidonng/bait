import { copyFile, exists, rm } from "node:fs/promises";
import tailwind from "bun-plugin-tailwind";

await rm("./dist", { recursive: true, force: true });

const result = await Bun.build({
  entrypoints: ["./index.html"],
  outdir: "./dist",
  minify: true,
  plugins: [tailwind],
});

if (!result.success) {
  for (const log of result.logs) console.error(log);
  process.exit(1);
}

if (await exists("./bait.wasm")) {
  await copyFile("./bait.wasm", "./dist/bait.wasm");
}
