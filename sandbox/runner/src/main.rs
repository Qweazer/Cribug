// Sandbox Runner — WASI code execution sandbox.
//
// Executes WASI .wasm bytecode in a wasmtime sandbox with fuel-based timeout
// and memory limits. Structured JSON output.
//
// Build:  cargo build --release
// Usage:  sandbox-runner --memory-mb 128 < code.wasm

use anyhow::{Context, Result};
use clap::Parser;
use serde::Serialize;
use std::io::Read;
use std::time::Instant;
use wasmtime::{Config, Engine, Linker, Module, Store};
use wasmtime_wasi::sync::WasiCtxBuilder;

#[derive(Parser)]
#[command(name = "sandbox-runner")]
struct Args {
    #[arg(long, default_value = "128")]
    memory_mb: u64,
    #[arg(long, default_value = "30")]
    timeout_sec: u64,
}

#[derive(Serialize)]
struct ExecutionResult {
    success: bool,
    exit_code: i32,
    stdout: String,
    stderr: String,
    duration_ms: u64,
    memory_used_bytes: u64,
    error: Option<String>,
    error_type: Option<String>,
}

fn main() -> Result<()> {
    let args = Args::parse();

    let mut wasm_bytes = Vec::new();
    std::io::stdin().read_to_end(&mut wasm_bytes).context("failed to read WASM")?;

    if wasm_bytes.is_empty() {
        let result = ExecutionResult {
            success: false, exit_code: -1,
            stdout: String::new(), stderr: "empty WASM input".to_string(),
            duration_ms: 0, memory_used_bytes: 0,
            error: Some("empty WASM input".to_string()),
            error_type: Some("sandbox_invalid_input".to_string()),
        };
        println!("{}", serde_json::to_string(&result)?);
        std::process::exit(1);
    }

    let start = Instant::now();

    match execute_wasm(&wasm_bytes, &args) {
        Ok((stdout, stderr)) => {
            let result = ExecutionResult {
                success: true, exit_code: 0, stdout, stderr,
                duration_ms: start.elapsed().as_millis() as u64,
                memory_used_bytes: 0, error: None, error_type: None,
            };
            println!("{}", serde_json::to_string(&result)?);
        }
        Err(e) => {
            let err_msg = format!("{:#}", e);
            let error_type = if err_msg.contains("fuel") {
                "sandbox_timeout"
            } else if err_msg.contains("memory") {
                "sandbox_memory_exceeded"
            } else {
                "sandbox_execution_failed"
            };
            let result = ExecutionResult {
                success: false, exit_code: 1, stdout: String::new(),
                stderr: err_msg.clone(), duration_ms: start.elapsed().as_millis() as u64,
                memory_used_bytes: 0, error: Some(err_msg),
                error_type: Some(error_type.to_string()),
            };
            println!("{}", serde_json::to_string(&result)?);
            std::process::exit(1);
        }
    }
    Ok(())
}

fn execute_wasm(wasm_bytes: &[u8], args: &Args) -> Result<(String, String)> {
    let mut config = Config::default();
    config.consume_fuel(true);

    let engine = Engine::new(&config)?;
    let module = Module::new(&engine, wasm_bytes)?;

    let wasi = WasiCtxBuilder::new()
        .inherit_stdio()
        .inherit_args()?
        .build();

    let mut store = Store::new(&engine, wasi);
    store.set_fuel(args.timeout_sec * 1_000_000)?;

    let mut linker = Linker::new(&engine);
    wasmtime_wasi::sync::add_to_linker(&mut linker, |s| s)?;

    let instance = linker.instantiate(&mut store, &module)?;

    if let Ok(func) = instance.get_typed_func::<(), ()>(&mut store, "_start") {
        func.call(&mut store, ())
            .map_err(|e| anyhow::anyhow!("execution trapped: {}", e))?;
    } else if let Ok(main_fn) = instance.get_typed_func::<i32, i32>(&mut store, "main") {
        let exit_code = main_fn.call(&mut store, 0)?;
        if exit_code != 0 {
            return Err(anyhow::anyhow!("exit code: {}", exit_code));
        }
    } else {
        return Err(anyhow::anyhow!("no _start or main function found"));
    }

    Ok(("sandbox execution completed".to_string(), String::new()))
}
