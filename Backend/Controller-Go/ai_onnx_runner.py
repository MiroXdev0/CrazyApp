"""Small, real ONNX Runtime entrypoint shipped in generated Nodren packages.

The worker executes this file with its normal SCRIPT task path. It deliberately
does not manufacture inputs: callers must provide JSON/NPY/NPZ input data for
models that require it.
"""

import argparse
import json
import os
import sys


def fail(message, args=None):
    if args is not None:
        message = (
            f"model={args.model}; input={args.input or '<none>'}; "
            f"worker={os.environ.get('NODREN_AI_WORKER_ID', '<unknown>')}; "
            f"runtime=onnxruntime; device={args.device}; reason={message}"
        )
    print("nodren onnxruntime: " + message, file=sys.stderr)
    return 2


def tensor_dtype(type_name, numpy):
    return {
        "tensor(float)": numpy.float32,
        "tensor(float16)": numpy.float16,
        "tensor(double)": numpy.float64,
        "tensor(int8)": numpy.int8,
        "tensor(uint8)": numpy.uint8,
        "tensor(int16)": numpy.int16,
        "tensor(uint16)": numpy.uint16,
        "tensor(int32)": numpy.int32,
        "tensor(uint32)": numpy.uint32,
        "tensor(int64)": numpy.int64,
        "tensor(uint64)": numpy.uint64,
        "tensor(bool)": numpy.bool_,
    }.get(type_name)


def tensor_type_name(value):
    return value.split("(", 1)[1].rstrip(")") if value.startswith("tensor(") else value


def validate_shape(name, value, metadata):
    actual = tuple(int(item) for item in value.shape)
    expected = metadata.shape
    if len(actual) != len(expected):
        raise ValueError(f"input {name!r} has rank {len(actual)}, expected {len(expected)}")
    for index, (actual_dimension, expected_dimension) in enumerate(zip(actual, expected)):
        if isinstance(expected_dimension, int) and expected_dimension > 0 and actual_dimension != expected_dimension:
            raise ValueError(f"input {name!r} dimension {index} is {actual_dimension}, expected {expected_dimension}")


def validate_dtype(name, value, metadata):
    expected = tensor_type_name(metadata.type)
    actual = value.dtype.kind
    allowed = {
        "float": "f",
        "float16": "f",
        "double": "f",
        "int8": "i",
        "int16": "i",
        "int32": "i",
        "int64": "i",
        "uint8": "u",
        "uint16": "u",
        "uint32": "u",
        "uint64": "u",
        "bool": "b",
    }
    expected_kind = allowed.get(expected)
    if expected_kind is None:
        raise ValueError(f"input {name!r} uses unsupported ONNX datatype {metadata.type}")
    if actual != expected_kind:
        raise ValueError(f"input {name!r} has datatype {value.dtype}, expected tensor({expected})")


def validate_inputs(inputs, session):
    metadata = {item.name: item for item in session.get_inputs()}
    missing = sorted(set(metadata) - set(inputs))
    extra = sorted(set(inputs) - set(metadata))
    if missing:
        raise ValueError("missing model inputs: " + ", ".join(missing))
    if extra:
        raise ValueError("unknown model inputs: " + ", ".join(extra))
    for name, value in inputs.items():
        validate_dtype(name, value, metadata[name])
        validate_shape(name, value, metadata[name])


def json_tensor(raw, metadata, numpy, name):
    if isinstance(raw, bool):
        source = numpy.asarray(raw)
    elif isinstance(raw, (int, float)):
        source = numpy.asarray(raw)
    else:
        source = numpy.asarray(raw)
    expected = tensor_type_name(metadata.type)
    if expected in ("float", "float16", "double"):
        if source.dtype.kind not in ("i", "u", "f"):
            raise ValueError(f"input {name!r} JSON value is not numeric")
    elif expected.startswith("int") or expected.startswith("uint"):
        if source.dtype.kind not in ("i", "u"):
            raise ValueError(f"input {name!r} JSON value must contain integers")
    elif expected == "bool" and source.dtype.kind != "b":
        raise ValueError(f"input {name!r} JSON value must contain booleans")
    dtype = tensor_dtype(metadata.type, numpy)
    return numpy.asarray(raw, dtype=dtype)


def load_inputs(path, session, numpy):
    if not path:
        raise ValueError("model input is required; pass --input <json|npy|npz>")
    extension = os.path.splitext(path)[1].lower()
    metadata = {item.name: item for item in session.get_inputs()}
    if extension == ".json":
        with open(path, "r", encoding="utf-8") as stream:
            value = json.load(stream)
        if not isinstance(value, dict):
            if len(metadata) != 1:
                raise ValueError("JSON input must be an object keyed by model input name")
            value = {next(iter(metadata)): value}
        inputs = {}
        for name, raw in value.items():
            if name not in metadata:
                raise ValueError("unknown model input: " + name)
            inputs[name] = json_tensor(raw, metadata[name], numpy, name)
        validate_inputs(inputs, session)
        return inputs
    if extension == ".npy":
        if len(metadata) != 1:
            raise ValueError("NPY input is supported only for single-input models")
        inputs = {next(iter(metadata)): numpy.load(path, allow_pickle=False)}
        validate_inputs(inputs, session)
        return inputs
    if extension == ".npz":
        archive = numpy.load(path, allow_pickle=False)
        inputs = {name: archive[name] for name in metadata if name in archive}
        validate_inputs(inputs, session)
        return inputs
    raise ValueError("unsupported input format; use JSON, NPY, or NPZ")


def json_value(value):
    if hasattr(value, "tolist"):
        return value.tolist()
    return value


def output_metadata(session):
    return [
        {"name": item.name, "type": item.type, "shape": list(item.shape)}
        for item in session.get_outputs()
    ]


def output_payload(session, outputs, numpy):
    metadata = output_metadata(session)
    return {
        item["name"]: json_value(value)
        for item, value in zip(metadata, outputs)
    }


def write_outputs(path, session, outputs, numpy):
    if not path:
        return None
    extension = os.path.splitext(path)[1].lower()
    names = [item.name for item in session.get_outputs()]
    if extension == ".json":
        with open(path, "w", encoding="utf-8") as stream:
            json.dump({"outputs": output_payload(session, outputs, numpy), "metadata": output_metadata(session)}, stream, separators=(",", ":"))
    elif extension == ".npy":
        if len(outputs) != 1:
            raise ValueError("NPY output requires a single model output; use NPZ for multiple outputs")
        numpy.save(path, outputs[0], allow_pickle=False)
    elif extension == ".npz":
        numpy.savez(path, **{name: value for name, value in zip(names, outputs)})
    else:
        raise ValueError("unsupported output format; use JSON, NPY, or NPZ")
    return {"path": path, "format": extension[1:]}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True)
    parser.add_argument("--input")
    parser.add_argument("--output")
    parser.add_argument("--device", choices=("auto", "cpu", "gpu", "cuda"), default=os.environ.get("NODREN_AI_DEVICE", "auto"))
    args = parser.parse_args()

    try:
        import numpy
        import onnxruntime as ort
    except ImportError as error:
        return fail("requires the 'onnxruntime' Python package (and its NumPy dependency): " + str(error), args)

    available = list(ort.get_available_providers())
    wants_gpu = args.device in ("gpu", "cuda")
    has_cuda = "CUDAExecutionProvider" in available
    if wants_gpu and not has_cuda:
        return fail("GPU execution was requested, but CUDAExecutionProvider is unavailable; install onnxruntime-gpu and a compatible CUDA stack", args)

    providers = ["CUDAExecutionProvider", "CPUExecutionProvider"] if (has_cuda and args.device == "auto") else (["CUDAExecutionProvider"] if wants_gpu else ["CPUExecutionProvider"])
    options = ort.SessionOptions()
    thread_text = os.environ.get("NODREN_AI_CPU_THREADS", "")
    if thread_text.isdigit() and int(thread_text) > 0:
        options.intra_op_num_threads = int(thread_text)
        options.inter_op_num_threads = 1

    fallback = False
    try:
        session = ort.InferenceSession(args.model, sess_options=options, providers=providers)
    except Exception as error:
        if args.device != "auto" or not has_cuda:
            return fail("could not create inference session: " + str(error), args)
        try:
            session = ort.InferenceSession(args.model, sess_options=options, providers=["CPUExecutionProvider"])
            fallback = True
        except Exception as cpu_error:
            return fail("CUDA provider failed and CPU fallback also failed: " + str(cpu_error), args)

    try:
        inputs = load_inputs(args.input, session, numpy)
        outputs = session.run(None, inputs)
        output_file = write_outputs(args.output, session, outputs, numpy)
    except Exception as error:
        return fail("inference failed: " + str(error), args)

    result = {
        "runtime": "onnxruntime",
        "device": "cpu" if session.get_providers() == ["CPUExecutionProvider"] else "cuda",
        "gpu_index": os.environ.get("NODREN_AI_GPU_INDEX") if session.get_providers() != ["CPUExecutionProvider"] else None,
        "providers": session.get_providers(),
        "fallback": fallback,
        "outputs": output_payload(session, outputs, numpy) if not output_file else None,
        "output_metadata": output_metadata(session),
    }
    if output_file:
        result["output_file"] = output_file
    encoded = json.dumps(result, separators=(",", ":"))
    print(encoded)
    return 0


if __name__ == "__main__":
    sys.exit(main())
