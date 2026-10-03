use std::{
    ffi::{CString, c_char, c_int, c_void},
    io,
    path::PathBuf,
    ptr::NonNull,
};

#[repr(C)]
struct CoreConfig {
    worker_count: usize,
    queue_capacity: usize,
}

#[repr(C)]
struct CoreWorkloadResult {
    task_id: u64,
    value: i64,
    state: c_int,
    error_code: c_int,
}

type Create = unsafe extern "C" fn(*const CoreConfig) -> *mut c_void;
type Initialize = unsafe extern "C" fn(*mut c_void) -> c_int;
type Destroy = unsafe extern "C" fn(*mut c_void);
type ExecuteWorkload = unsafe extern "C" fn(
    *mut c_void,
    u64,
    *const c_char,
    *const u8,
    usize,
    *mut CoreWorkloadResult,
) -> c_int;

struct Symbols {
    create: Create,
    initialize: Initialize,
    destroy: Destroy,
    execute_workload: ExecuteWorkload,
}

#[derive(Debug)]
pub struct NativeWorkloadError {
    pub code: c_int,
    pub message: String,
}

pub struct NativeCore {
    handle: NonNull<c_void>,
    engine: NonNull<c_void>,
    symbols: Symbols,
}

// SAFETY: the native engine is initialized before it is shared, never shut
// down while worker threads can use it, and execute_workload only reads the
// immutable engine state while using task-local workload buffers.
unsafe impl Send for NativeCore {}
unsafe impl Sync for NativeCore {}

impl NativeCore {
    pub fn load() -> io::Result<Self> {
        Self::load_with_worker_count(0)
    }

    pub fn load_with_worker_count(worker_count: usize) -> io::Result<Self> {
        let path = resolve_core_library()?;
        let path = path.to_str().ok_or_else(|| {
            io::Error::new(io::ErrorKind::InvalidInput, "native core path is not UTF-8")
        })?;
        let handle = load_library(path)?;
        let symbols = match load_symbols(handle) {
            Ok(symbols) => symbols,
            Err(error) => {
                unload_library(handle);
                return Err(error);
            }
        };

        let config = CoreConfig {
            worker_count,
            queue_capacity: 4096,
        };
        let engine_ptr = unsafe { (symbols.create)(&config) };
        let engine = match NonNull::new(engine_ptr) {
            Some(engine) => engine,
            None => {
                unload_library(handle);
                return Err(io::Error::other("native core creation failed"));
            }
        };
        if unsafe { (symbols.initialize)(engine.as_ptr()) } != 0 {
            unsafe { (symbols.destroy)(engine.as_ptr()) };
            unload_library(handle);
            return Err(io::Error::other("native core initialization failed"));
        }

        Ok(Self {
            handle,
            engine,
            symbols,
        })
    }

    pub fn execute(
        &self,
        task_id: u64,
        command: &str,
        payload: &[u8],
    ) -> Result<i64, NativeWorkloadError> {
        let command_name = command.to_string();
        let command = CString::new(command).map_err(|_| NativeWorkloadError {
            code: 1,
            message: "workload command contains a NUL byte".to_string(),
        })?;
        let mut result = CoreWorkloadResult {
            task_id: 0,
            value: 0,
            state: 0,
            error_code: 1,
        };
        let status = unsafe {
            (self.symbols.execute_workload)(
                self.engine.as_ptr(),
                task_id,
                command.as_ptr(),
                if payload.is_empty() {
                    std::ptr::null()
                } else {
                    payload.as_ptr()
                },
                payload.len(),
                &mut result,
            )
        };
        if status == 0 && result.state == 2 {
            return Ok(result.value);
        }
        Err(NativeWorkloadError {
            code: result.error_code,
            message: native_error_message(result.error_code, command_name),
        })
    }
}

fn resolve_core_library() -> io::Result<PathBuf> {
    if let Ok(path) = std::env::var("NODREN_CORE_LIBRARY") {
        let path = PathBuf::from(path);
        if path.exists() {
            return Ok(path);
        }
    }

    let library_names: &[&str] = if cfg!(windows) {
        &["norden-core.dll", "nodren-core.dll", "nodren_core.dll"]
    } else if cfg!(target_os = "macos") {
        &["libnodren_core.dylib"]
    } else {
        &["libnodren_core.so"]
    };
    if let Ok(executable) = std::env::current_exe() {
        if let Some(directory) = executable.parent() {
            for library_name in library_names {
                let path = directory.join(library_name);
                if path.exists() {
                    return Ok(path);
                }
            }
        }
    }

    if let Some(path) = option_env!("NODREN_CORE_LIBRARY") {
        let path = PathBuf::from(path);
        if path.exists() {
            return Ok(path);
        }
    }

    Err(io::Error::new(
        io::ErrorKind::NotFound,
        format!(
            "native core library not found; expected one of {} beside the worker executable",
            library_names.join(", ")
        ),
    ))
}

fn native_error_message(code: c_int, command: String) -> String {
    match code {
        1 => "invalid workload arguments".to_string(),
        2 => format!("malformed payload for workload {command}"),
        3 => format!("unsupported workload: {command}"),
        4 => format!("native execution failed for workload {command}"),
        _ => format!("native core failure for workload {command}"),
    }
}

impl Drop for NativeCore {
    fn drop(&mut self) {
        unsafe { (self.symbols.destroy)(self.engine.as_ptr()) };
        unload_library(self.handle);
    }
}

#[cfg(windows)]
fn load_library(path: &str) -> io::Result<NonNull<c_void>> {
    use std::{os::windows::ffi::OsStrExt, path::Path};
    let wide: Vec<u16> = Path::new(path)
        .as_os_str()
        .encode_wide()
        .chain(Some(0))
        .collect();
    let handle = unsafe { LoadLibraryW(wide.as_ptr()) } as *mut c_void;
    NonNull::new(handle).ok_or_else(io::Error::last_os_error)
}

#[cfg(windows)]
fn load_symbols(handle: NonNull<c_void>) -> io::Result<Symbols> {
    unsafe {
        Ok(Symbols {
            create: symbol(handle, "nodren_core_create")?,
            initialize: symbol(handle, "nodren_core_initialize")?,
            destroy: symbol(handle, "nodren_core_destroy")?,
            execute_workload: symbol(handle, "nodren_core_execute_workload")?,
        })
    }
}

#[cfg(windows)]
unsafe fn symbol<T>(handle: NonNull<c_void>, name: &str) -> io::Result<T> {
    let name = CString::new(name).unwrap();
    let ptr =
        unsafe { GetProcAddress(handle.as_ptr(), name.as_ptr() as *const c_char) } as *const ();
    if ptr.is_null() {
        return Err(io::Error::new(
            io::ErrorKind::NotFound,
            format!("native core symbol missing: {name:?}"),
        ));
    }
    Ok(unsafe { std::mem::transmute_copy(&ptr) })
}

#[cfg(windows)]
fn unload_library(handle: NonNull<c_void>) {
    unsafe { FreeLibrary(handle.as_ptr()) };
}

#[cfg(windows)]
#[link(name = "kernel32")]
unsafe extern "system" {
    fn LoadLibraryW(name: *const u16) -> *mut c_void;
    fn GetProcAddress(handle: *mut c_void, name: *const c_char) -> *mut c_void;
    fn FreeLibrary(handle: *mut c_void) -> i32;
}

#[cfg(unix)]
fn load_library(path: &str) -> io::Result<NonNull<c_void>> {
    let path = CString::new(path).map_err(|_| io::Error::other("invalid native core path"))?;
    let handle = unsafe { dlopen(path.as_ptr(), 2) };
    NonNull::new(handle).ok_or_else(|| io::Error::other("failed to load native core"))
}

#[cfg(unix)]
fn load_symbols(handle: NonNull<c_void>) -> io::Result<Symbols> {
    unsafe {
        Ok(Symbols {
            create: symbol(handle, "nodren_core_create")?,
            initialize: symbol(handle, "nodren_core_initialize")?,
            destroy: symbol(handle, "nodren_core_destroy")?,
            execute_workload: symbol(handle, "nodren_core_execute_workload")?,
        })
    }
}

#[cfg(unix)]
unsafe fn symbol<T>(handle: NonNull<c_void>, name: &str) -> io::Result<T> {
    let name = CString::new(name).unwrap();
    let ptr = unsafe { dlsym(handle.as_ptr(), name.as_ptr()) };
    if ptr.is_null() {
        return Err(io::Error::new(
            io::ErrorKind::NotFound,
            format!("native core symbol missing: {name:?}"),
        ));
    }
    Ok(unsafe { std::mem::transmute_copy(&ptr) })
}

#[cfg(unix)]
fn unload_library(handle: NonNull<c_void>) {
    unsafe { dlclose(handle.as_ptr()) };
}

#[cfg(unix)]
#[link(name = "dl")]
unsafe extern "C" {
    fn dlopen(path: *const c_char, flags: c_int) -> *mut c_void;
    fn dlsym(handle: *mut c_void, name: *const c_char) -> *mut c_void;
    fn dlclose(handle: *mut c_void) -> c_int;
}
