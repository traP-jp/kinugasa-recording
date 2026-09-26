use std::sync::Arc;

use kinugasa_core::{
    application::{
        CameraUseCases, LockfileUseCases, PreviewUseCases, SessionUseCases, StatisticsUseCases,
        TakeUseCases,
    },
    ports::UuidV7IdGenerator,
};
use kinugasa_media::MediaService;
use kinugasa_mysql::MySqlRepository;

use crate::SystemClock;

pub type SessionService =
    SessionUseCases<MySqlRepository, MySqlRepository, SystemClock, UuidV7IdGenerator>;
pub type CameraService =
    CameraUseCases<MySqlRepository, MySqlRepository, SystemClock, UuidV7IdGenerator>;
pub type TakeService =
    TakeUseCases<MySqlRepository, MySqlRepository, MediaService, SystemClock, UuidV7IdGenerator>;
pub type PreviewService = PreviewUseCases<MySqlRepository, MySqlRepository, MediaService>;
pub type StatisticsService = StatisticsUseCases<MySqlRepository, MySqlRepository, MediaService>;
pub type LockfileService = LockfileUseCases<MySqlRepository, MySqlRepository>;

/// DI-complete application surface consumed by inbound adapters such as HTTP.
#[derive(Clone)]
pub struct Services {
    pub sessions: Arc<SessionService>,
    pub cameras: Arc<CameraService>,
    pub takes: Arc<TakeService>,
    pub previews: Arc<PreviewService>,
    pub statistics: Arc<StatisticsService>,
    pub lockfiles: Arc<LockfileService>,
}
