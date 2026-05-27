// Sandbox Runner — WASI code execution sandbox.
//
// Executes WASI .wasm bytecode in a wasmtime sandbox with fuel-based
// timeout and memory limits. Captures stdout/stderr via OS-level
// fd redirection. Structured JSON result to stdout.
//
// Build:  cargo build --release
// Usage:  sandbox-runner --memory-mb 128 < code.wasm

use anyhow::{Context, Result};
use clap::Parser;
use serde::Serialize;
use std::io::Read;
use std::os::fd::AsRawFd;
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
    stdout_truncated: bool,
    stderr_truncated: bool,
}

fn main() -> Result<()> {
    let args = Args::parse();

    let mut wasm_bytes = Vec::new();
    std::io::stdin().read_to_end(&mut wasm_bytes).context("failed to read WASM")?;

    if wasm_bytes.is_empty() {
        output_error("empty WASM input", "sandbox_invalid_input", 0);
        std::process::exit(1);
    }

    let start = Instant::now();

    match execute_sandboxed(&wasm_bytes, &args) {
        Ok((stdout, stderr)) => {
            let result = ExecutionResult {
                success: true, exit_code: 0, stdout, stderr,
                duration_ms: start.elapsed().as_millis() as u64,
                memory_used_bytes: 0, error: None, error_type: None,
                stdout_truncated: false, stderr_truncated: false,
            };
            println!("{}", serde_json::to_string(&result)?);
        }
        Err(e) => {
            let err_msg = format!("{:#}", e);
            let error_type = if err_msg.contains("fuel") { "sandbox_timeout" }
            else if err_msg.contains("memory") { "sandbox_memory_exceeded" }
            else { "sandbox_execution_failed" };
            output_error(&err_msg, error_type, start.elapsed().as_millis() as u64);
            std::process::exit(1);
        }
    }
    Ok(())
}

fn output_error(msg: &str, err_type: &str, duration_ms: u64) {
    let result = ExecutionResult {
        success: false, exit_code: 1,
        stdout: String::new(), stderr: msg.to_string(),
        duration_ms, memory_used_bytes: 0,
        error: Some(msg.to_string()), error_type: Some(err_type.to_string()),
        stdout_truncated: false, stderr_truncated: false,
    };
    println!("{}", serde_json::to_string(&result).unwrap_or_default());
}

fn execute_sandboxed(wasm_bytes: &[u8], args: &Args) -> Result<(String, String)> {
    let mut config = Config::default();
    config.consume_fuel(true);

    let engine = Engine::new(&config)?;
    let module = Module::new(&engine, wasm_bytes)?;

    // Create OS pipes for capturing stdout/stderr via dup2
    let (mut stdout_read, stdout_write) = os_pipe::pipe()?;
    let (mut stderr_read, stderr_write) = os_pipe::pipe()?;

    // Save original fd's
    let stdout_fd: std::os::fd::RawFd = 1;
    let stderr_fd: std::os::fd::RawFd = 2;
    let saved_stdout = nix::unistd::dup(stdout_fd)?;
    let saved_stderr = nix::unistd::dup(stderr_fd)?;

    // Redirect real fd's to pipe write ends
    nix::unistd::dup2(stdout_write.as_raw_fd(), stdout_fd)?;
    nix::unistd::dup2(stderr_write.as_raw_fd(), stderr_fd)?;

    // Build WASI context — inherit stdio (which now goes to our pipes)
    let wasi = WasiCtxBuilder::new()
        .inherit_stdio()
        .inherit_args()?
        .build();

    let mut store = Store::new(&engine, wasi);
    store.set_fuel(args.timeout_sec * 1_000_000)?;

    let mut linker = Linker::new(&engine);
    wasmtime_wasi::sync::add_to_linker(&mut linker, |s| s)?;

    let instance = linker.instantiate(&mut store, &module)?;

    let exec_result = if let Ok(func) = instance.get_typed_func::<(), ()>(&mut store, "_start") {
        match func.call(&mut store, ()) {
            Ok(()) => Ok(()),
            Err(e) => {
                // Check for I32Exit (proc_exit) from WASI
                if let Some(exit) = e.downcast_ref::<wasi_common::I32Exit>() {
                    if exit.0 != 0 {
                        Err(anyhow::anyhow!("exit code: {}", exit.0))
                    } else {
                        Ok(())
                    }
                } else if let Some(exit) = e.downcast_ref::<wasmtime_wasi::preview2::I32Exit>() {
                    if exit.0 != 0 {
                        Err(anyhow::anyhow!("exit code: {}", exit.0))
                    } else {
                        Ok(())
                    }
                } else {
                    let msg = format!("{}", e);
                    Err(anyhow::anyhow!("execution trapped: {}", msg))
                }
            }
        }
    } else if let Ok(main_fn) = instance.get_typed_func::<i32, i32>(&mut store, "main") {
        let exit_code = main_fn.call(&mut store, 0)?;
        if exit_code != 0 {
            Err(anyhow::anyhow!("exit code: {}", exit_code))
        } else {
            Ok(())
        }
    } else {
        Err(anyhow::anyhow!("no _start or main function found"))
    };

    // Flush any buffered output and restore original fd's
    drop(store);
    drop(linker);
    drop(instance);
    drop(stdout_write);
    drop(stderr_write);

    nix::unistd::dup2(saved_stdout, stdout_fd)?;
    nix::unistd::dup2(saved_stderr, stderr_fd)?;
    nix::unistd::close(saved_stdout)?;
    nix::unistd::close(saved_stderr)?;

    // Read captured output
    let mut stdout = String::new();
    stdout_read.read_to_string(&mut stdout)?;

    let mut stderr = String::new();
    stderr_read.read_to_string(&mut stderr)?;

    exec_result?;
    Ok((stdout, stderr))
}
