use std::{env, fs, path::PathBuf, process::Command};

fn run(program: &str, args: &[String]) {
    let status = Command::new(program)
        .args(args)
        .status()
        .unwrap_or_else(|error| panic!("failed to start {program}: {error}"));
    if !status.success() {
        panic!("{program} failed with status {status}");
    }
}

fn main() {
    let root = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").unwrap());
    let core = root.join("..").join("..").join("Core");
    let out = PathBuf::from(env::var_os("OUT_DIR").unwrap()).join("nodren-core");
    fs::create_dir_all(&out).expect("create native core build directory");

    let include_c = core.join("C").join("include");
    let include_cpp = core.join("Cpp").join("include");
    let source_c = core.join("C").join("src").join("nodren_memory.c");
    let sources_cpp = [
        core.join("Cpp").join("src").join("compute_kernel.cpp"),
        core.join("Cpp").join("src").join("thread_pool.cpp"),
        core.join("Cpp").join("src").join("core_engine.cpp"),
        core.join("Cpp").join("src").join("nodren_c_api.cpp"),
        core.join("Cpp").join("src").join("workload_dispatch.cpp"),
    ];
    let windows = env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("windows");
    let asm = if windows {
        core.join("Assembly")
            .join("X64")
            .join("Windows")
            .join("sum_avx2.S")
    } else {
        core.join("Assembly")
            .join("X64")
            .join("Linux")
            .join("sum_avx2.S")
    };

    println!("cargo:rerun-if-changed={}", source_c.display());
    for source in sources_cpp.iter().chain(std::iter::once(&asm)) {
        println!("cargo:rerun-if-changed={}", source.display());
    }

    let c_object = out.join("nodren_memory.o");
    let mut c_args = vec![
        "-std=c11".into(),
        "-O3".into(),
        "-DNDEBUG".into(),
        "-I".into(),
        include_c.display().to_string(),
        "-c".into(),
        source_c.display().to_string(),
        "-o".into(),
        c_object.display().to_string(),
    ];
    if !windows {
        c_args.insert(4, "-fPIC".into());
    }
    run("gcc", &c_args);

    let mut objects = vec![c_object];
    for (index, source) in sources_cpp.iter().enumerate() {
        let object = out.join(format!("core-{index}.o"));
        let mut args = vec![
            "-std=c++20".into(),
            "-O3".into(),
            "-DNDEBUG".into(),
            "-I".into(),
            include_c.display().to_string(),
            "-I".into(),
            include_cpp.display().to_string(),
            "-c".into(),
            source.display().to_string(),
            "-o".into(),
            object.display().to_string(),
        ];
        if !windows {
            args.insert(4, "-fPIC".into());
        }
        run("g++", &args);
        objects.push(object);
    }

    let asm_object = out.join("sum_avx2.o");
    run(
        "g++",
        &[
            "-c".into(),
            "-O3".into(),
            asm.display().to_string(),
            "-o".into(),
            asm_object.display().to_string(),
        ],
    );
    objects.push(asm_object);

    let library = if windows {
        out.join("nodren_core.dll")
    } else if env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("macos") {
        out.join("libnodren_core.dylib")
    } else {
        out.join("libnodren_core.so")
    };
    let mut link_args = vec!["-shared".into()];
    if windows {
        link_args.extend(["-static-libgcc".into(), "-static-libstdc++".into()]);
    }
    link_args.extend(
        objects
            .into_iter()
            .map(|object| object.display().to_string()),
    );
    link_args.extend(["-o".into(), library.display().to_string()]);
    run("g++", &link_args);
    println!("cargo:rustc-env=NODREN_CORE_LIBRARY={}", library.display());
}
