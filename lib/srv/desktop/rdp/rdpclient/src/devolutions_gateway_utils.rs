// todo(isaiah): some utils adapted from the devolutions-gateway repo, see if there's a way to put these in
// ironrdp, otherwise rename them.
use bytes::{Buf, BytesMut};
use ironrdp::pdu::connection_initiation::{NegotiationError, Response};
use ironrdp::pdu::PduParsing;
use ironrdp::rdcleanpath::RDCleanPathPdu;
use std::io;
use thiserror::Error;
use tokio::io::{AsyncRead, AsyncReadExt as _};
use tokio_util::codec::Decoder;

pub async fn read_cleanpath_pdu(
    stream: &mut (dyn AsyncRead + Unpin + Send),
) -> io::Result<RDCleanPathPdu> {
    let mut buf = bytes::BytesMut::new();

    // TODO: check if there is code to be reused from ironrdp code base for that
    let cleanpath_pdu = loop {
        if let Some(pdu) = RDCleanPathPdu::decode(&mut buf).map_err(|e| {
            io::Error::new(
                io::ErrorKind::InvalidInput,
                format!("bad RDCleanPathPdu: {e}"),
            )
        })? {
            break pdu;
        }

        let mut read_bytes = [0u8; 1024];
        let len = stream.read(&mut read_bytes[..]).await?;
        buf.extend_from_slice(&read_bytes[..len]);

        if len == 0 {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "EOF when reading RDCleanPathPdu",
            ));
        }
    };

    // Sanity check: make sure there is no leftover
    if !buf.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::Other,
            "no leftover is expected after reading cleanpath PDU",
        ));
    }

    Ok(cleanpath_pdu)
}

// todo(isaiah): below here is adapted from devolutions-gateway/src/transport/x224.rs
macro_rules! negotiation_try {
    ($e:expr) => {
        match $e {
            Ok(v) => v,
            Err(NegotiationError::IOError(ref e)) if e.kind() == io::ErrorKind::UnexpectedEof => {
                return Ok(None);
            }
            Err(e) => return Err(map_negotiation_error(e)),
        }
    };
}

#[derive(Default)]
pub struct NegotiationWithServerTransport;

impl Decoder for NegotiationWithServerTransport {
    type Item = Response;
    type Error = io::Error;

    fn decode(&mut self, buf: &mut BytesMut) -> Result<Option<Self::Item>, Self::Error> {
        let connection_response = negotiation_try!(Response::from_buffer(buf.as_ref()));
        buf.advance(connection_response.buffer_length());

        Ok(Some(connection_response))
    }
}

fn map_negotiation_error(e: NegotiationError) -> io::Error {
    match e {
        NegotiationError::ResponseFailure(e) => io::Error::new(
            io::ErrorKind::Other,
            format!("Negotiation Response error (code: {e:?})"),
        ),
        NegotiationError::TpktVersionError => io::Error::new(
            io::ErrorKind::InvalidData,
            "Negotiation invalid tpkt header version",
        ),
        NegotiationError::NotEnoughBytes => io::Error::new(
            io::ErrorKind::Other,
            "Negotiation not enough bytes to parse PDU",
        ),
        NegotiationError::IOError(e) => e,
    }
}

// todo(isaiah): copied from devolutions-gateway, we don't need most of these types and can
// probably remove thi
#[derive(Debug, Error)]
pub(crate) enum CleanPathError {
    #[error("bad request")]
    BadRequest(#[source] anyhow::Error),
    #[error("internal error")]
    Internal(#[from] anyhow::Error),
    #[error("Couldn’t perform TLS handshake")]
    TlsHandshake(#[source] io::Error),
    #[error("authorization error")]
    Authorization(#[from] AuthorizationError),
    #[error("Generic IO error")]
    Io(#[from] io::Error),
}

#[derive(Debug, Error)]
pub(crate) enum AuthorizationError {
    #[error("token not allowed")]
    Forbidden,
    #[error("token missing from request")]
    Unauthorized,
}
