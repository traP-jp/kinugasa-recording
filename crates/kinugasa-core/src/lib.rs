//! Domain types and implementation-independent ports for kinugasa recording.
//!
//! This crate deliberately contains no database, transport, filesystem, or
//! runtime implementation. Adapters for MySQL, PostgreSQL, MoQ, object storage,
//! and the local recorder depend on this crate, not the other way around.

#![forbid(unsafe_code)]

pub mod domain;
pub mod ports;
