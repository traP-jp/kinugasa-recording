use async_trait::async_trait;
use kinugasa_core::{
    domain::{Session, SessionName, SessionState},
    ports::{Page, PageRequest, RepositoryError, SessionDetail, SessionRepository},
};

use crate::{
    MySqlRepository, MySqlUnitOfWork,
    error::classify_sqlx,
    model::{SessionRow, naive},
};

#[async_trait]
impl SessionRepository<MySqlUnitOfWork> for MySqlRepository {
    async fn create_session(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        session: &Session,
    ) -> Result<(), RepositoryError> {
        sqlx::query("INSERT INTO sessions (id, name, state, created_at) VALUES (?, ?, ?, ?)")
            .bind(session.id().into_uuid())
            .bind(session.name().as_str())
            .bind(session_state(session.state()))
            .bind(naive(session.created_at()))
            .execute(&mut *unit_of_work.connection()?)
            .await
            .map_err(classify_sqlx)?;
        Ok(())
    }

    async fn list_sessions(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        page: PageRequest,
    ) -> Result<Page<Session>, RepositoryError> {
        let connection = unit_of_work.connection()?;
        let total = sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM sessions")
            .fetch_one(&mut *connection)
            .await
            .map_err(classify_sqlx)?;
        let rows = sqlx::query_as::<_, SessionRow>(
            r#"
            SELECT id, CAST(name AS CHAR) AS name, CAST(state AS CHAR) AS state, created_at,
                   NULL AS ongoing_take_name
            FROM sessions
            ORDER BY created_at DESC, name ASC
            LIMIT ? OFFSET ?
            "#,
        )
        .bind(u64::from(page.page_size()))
        .bind(page.offset())
        .fetch_all(&mut *connection)
        .await
        .map_err(classify_sqlx)?;

        let items = rows
            .into_iter()
            .map(|row| row.into_detail().map(|detail| detail.session))
            .collect::<Result<Vec<_>, _>>()?;
        Ok(Page {
            items,
            total: u64::try_from(total).map_err(crate::error::corrupt)?,
        })
    }

    async fn get_session(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        name: &SessionName,
    ) -> Result<SessionDetail, RepositoryError> {
        sqlx::query_as::<_, SessionRow>(
            r#"
            SELECT s.id, CAST(s.name AS CHAR) AS name, CAST(s.state AS CHAR) AS state, s.created_at,
                   CAST(t.name AS CHAR) AS ongoing_take_name
            FROM sessions AS s
            LEFT JOIN takes AS t
              ON t.session_id = s.id AND t.phase = 'ongoing'
            WHERE s.name = ?
            "#,
        )
        .bind(name.as_str())
        .fetch_optional(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?
        .ok_or(RepositoryError::NotFound)?
        .into_detail()
    }

    async fn get_session_for_update(
        &self,
        unit_of_work: &mut MySqlUnitOfWork,
        name: &SessionName,
    ) -> Result<SessionDetail, RepositoryError> {
        sqlx::query_as::<_, SessionRow>(
            r#"
            SELECT s.id, CAST(s.name AS CHAR) AS name, CAST(s.state AS CHAR) AS state, s.created_at,
                   CAST(t.name AS CHAR) AS ongoing_take_name
            FROM sessions AS s
            LEFT JOIN takes AS t
              ON t.session_id = s.id AND t.phase = 'ongoing'
            WHERE s.name = ?
            FOR UPDATE
            "#,
        )
        .bind(name.as_str())
        .fetch_optional(&mut *unit_of_work.connection()?)
        .await
        .map_err(classify_sqlx)?
        .ok_or(RepositoryError::NotFound)?
        .into_detail()
    }
}

const fn session_state(state: SessionState) -> &'static str {
    match state {
        SessionState::Active => "active",
        SessionState::Inactive => "inactive",
    }
}
