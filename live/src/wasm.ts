import { File, OpenFile, WASI } from "@bjorn3/browser_wasi_shim";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export interface TranslateResult {
  stdout: string;
  stderr: string;
}

/**
 * Runs the compiled bait WASI module against the given Bash input.
 */
export async function translateWithWasm(
  wasmModule: WebAssembly.Module,
  input: string,
): Promise<TranslateResult> {
  const stdinFile = new File(encoder.encode(input));
  const stdoutFile = new File(new Uint8Array());
  const stderrFile = new File(new Uint8Array());

  const wasi = new WASI(
    ["bait"],
    ["NO_COLOR=1"],
    [
      new OpenFile(stdinFile),
      new OpenFile(stdoutFile),
      new OpenFile(stderrFile),
    ],
  );

  const instance = await WebAssembly.instantiate(wasmModule, {
    wasi_snapshot_preview1: wasi.wasiImport as WebAssembly.ModuleImports,
  });
  wasi.start(instance as Parameters<typeof wasi.start>[0]);

  return {
    stdout: decoder.decode(stdoutFile.data),
    stderr: decoder.decode(stderrFile.data),
  };
}
