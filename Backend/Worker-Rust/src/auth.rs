use hmac::{Hmac, Mac};
use sha2::Sha256;
use std::io::{self, Read};

type HmacSha256 = Hmac<Sha256>;

fn mac(key: &[u8], parts: &[&[u8]]) -> io::Result<[u8; 32]> {
    let mut mac = HmacSha256::new_from_slice(key)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "invalid authentication key"))?;
    for part in parts {
        mac.update(part);
    }
    Ok(mac.finalize().into_bytes().into())
}

pub fn worker_proof(secret: &[u8], nonce: &[u8], worker_id: &str) -> io::Result<[u8; 32]> {
    mac(
        secret,
        &[b"NODREN-WORKER-LOGIN-v1\0", nonce, worker_id.as_bytes()],
    )
}

pub fn session_key(secret: &[u8], nonce: &[u8], worker_id: &str) -> io::Result<[u8; 32]> {
    mac(
        secret,
        &[b"NODREN-SESSION-KEY-v1\0", nonce, worker_id.as_bytes()],
    )
}

pub fn controller_proof(key: &[u8], nonce: &[u8], worker_id: &str) -> io::Result<[u8; 32]> {
    mac(
        key,
        &[b"NODREN-CONTROLLER-LOGIN-v1\0", nonce, worker_id.as_bytes()],
    )
}

pub fn verify_controller_proof(
    key: &[u8],
    nonce: &[u8],
    worker_id: &str,
    proof: &[u8],
) -> io::Result<()> {
    let expected = controller_proof(key, nonce, worker_id)?;
    let mut verifier = HmacSha256::new_from_slice(key)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "invalid authentication key"))?;
    verifier.update(b"NODREN-CONTROLLER-LOGIN-v1\0");
    verifier.update(nonce);
    verifier.update(worker_id.as_bytes());
    if proof.len() != expected.len() {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "Controller authentication failed",
        ));
    }
    verifier.verify_slice(proof).map_err(|_| {
        io::Error::new(
            io::ErrorKind::PermissionDenied,
            "Controller authentication failed",
        )
    })
}

pub fn protect_frame(
    key: &[u8],
    direction: u8,
    message_type: u16,
    request_id: u64,
    sequence: u64,
    payload: &[u8],
) -> io::Result<Vec<u8>> {
    let type_bytes = message_type.to_le_bytes();
    let request_bytes = request_id.to_le_bytes();
    let sequence_bytes = sequence.to_le_bytes();
    let tag = mac(
        key,
        &[
            b"NODREN-FRAME-v1\0",
            &[direction],
            &type_bytes,
            &request_bytes,
            &sequence_bytes,
            payload,
        ],
    )?;
    let mut protected = Vec::with_capacity(8 + payload.len() + tag.len());
    protected.extend_from_slice(&sequence_bytes);
    protected.extend_from_slice(payload);
    protected.extend_from_slice(&tag);
    Ok(protected)
}

pub fn verify_frame(
    key: &[u8],
    direction: u8,
    message_type: u16,
    request_id: u64,
    expected_sequence: u64,
    protected: &[u8],
) -> io::Result<Vec<u8>> {
    if protected.len() < 8 + 32 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "authenticated frame is truncated",
        ));
    }
    let sequence = u64::from_le_bytes(
        protected[..8]
            .try_into()
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid frame sequence"))?,
    );
    if sequence != expected_sequence {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "authenticated frame sequence mismatch",
        ));
    }
    let body_end = protected.len() - 32;
    let payload = &protected[8..body_end];
    let type_bytes = message_type.to_le_bytes();
    let request_bytes = request_id.to_le_bytes();
    let sequence_bytes = sequence.to_le_bytes();
    let mut verifier = HmacSha256::new_from_slice(key)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "invalid authentication key"))?;
    for part in [
        b"NODREN-FRAME-v1\0".as_slice(),
        &[direction],
        &type_bytes,
        &request_bytes,
        &sequence_bytes,
        payload,
    ] {
        verifier.update(part);
    }
    verifier.verify_slice(&protected[body_end..]).map_err(|_| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            "authenticated frame MAC mismatch",
        )
    })?;
    Ok(payload.to_vec())
}

pub fn read_session_frame<R: Read>(
    reader: &mut R,
    key: Option<&[u8; 32]>,
    sequence: &mut u64,
) -> io::Result<crate::protocol::Frame> {
    let mut frame = crate::protocol::read_frame(reader)?;
    if let Some(key) = key {
        let next = sequence.checked_add(1).ok_or_else(|| {
            io::Error::new(
                io::ErrorKind::InvalidData,
                "authenticated frame sequence exhausted",
            )
        })?;
        frame.payload = verify_frame(
            key,
            b'C',
            frame.typ as u16,
            frame.request_id,
            next,
            &frame.payload,
        )?;
        *sequence = next;
    }
    Ok(frame)
}

#[cfg(test)]
mod tests {
    use super::{protect_frame, verify_frame, worker_proof};

    #[test]
    fn authenticated_frames_reject_replay_and_tampering() {
        let key = [0x42; 32];
        let frame = protect_frame(&key, b'W', 6, 9, 1, b"task payload").unwrap();
        assert_eq!(
            verify_frame(&key, b'W', 6, 9, 1, &frame).unwrap(),
            b"task payload"
        );
        assert!(verify_frame(&key, b'W', 6, 9, 2, &frame).is_err());

        let mut tampered = frame;
        tampered[8] ^= 1;
        assert!(verify_frame(&key, b'W', 6, 9, 1, &tampered).is_err());
    }

    #[test]
    fn worker_proof_is_bound_to_the_worker_identity() {
        let secret = b"a sufficiently long test secret";
        let nonce = [7; 32];
        let proof = worker_proof(secret, &nonce, "worker-a").unwrap();
        assert_ne!(proof, worker_proof(secret, &nonce, "worker-b").unwrap());
    }
}
