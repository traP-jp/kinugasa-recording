//! Domain types and implementation-independent ports for kinugasa recording.
//!
//! This crate deliberately contains no database, transport, filesystem, or
//! runtime implementation. Persistence, media, object-storage, and recorder
//! adapters depend on this crate, not the other way around.

#![forbid(unsafe_code)]

pub mod application;
pub mod domain;
pub mod ports;
