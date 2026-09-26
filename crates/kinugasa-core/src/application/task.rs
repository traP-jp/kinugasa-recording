use tokio::task::JoinSet;

use crate::application::UseCaseError;

pub(crate) async fn collect_tasks<T: 'static>(
    mut tasks: JoinSet<Result<T, UseCaseError>>,
) -> Result<Vec<T>, UseCaseError> {
    let mut values = Vec::with_capacity(tasks.len());
    let mut first_error = None;

    while let Some(result) = tasks.join_next().await {
        match result {
            Ok(Ok(value)) => values.push(value),
            Ok(Err(error)) => {
                if first_error.is_none() {
                    first_error = Some(error);
                }
            }
            Err(error) => {
                if first_error.is_none() {
                    first_error = Some(UseCaseError::Task(error.to_string()));
                }
            }
        }
    }

    first_error.map_or(Ok(values), Err)
}

#[cfg(test)]
mod tests {
    use std::sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    };

    use super::*;

    #[test]
    fn tasks_run_concurrently_and_all_are_drained_after_an_error() {
        tokio::runtime::Builder::new_current_thread()
            .build()
            .unwrap()
            .block_on(async {
                let active = Arc::new(AtomicUsize::new(0));
                let peak = Arc::new(AtomicUsize::new(0));
                let completed = Arc::new(AtomicUsize::new(0));
                let mut tasks = JoinSet::new();

                for index in 0..4 {
                    let active = Arc::clone(&active);
                    let peak = Arc::clone(&peak);
                    let completed = Arc::clone(&completed);
                    tasks.spawn(async move {
                        let now_active = active.fetch_add(1, Ordering::SeqCst) + 1;
                        peak.fetch_max(now_active, Ordering::SeqCst);
                        tokio::task::yield_now().await;
                        active.fetch_sub(1, Ordering::SeqCst);
                        completed.fetch_add(1, Ordering::SeqCst);
                        if index == 0 {
                            Err(UseCaseError::Task("expected failure".to_owned()))
                        } else {
                            Ok(index)
                        }
                    });
                }

                let result = collect_tasks(tasks).await;
                assert!(result.is_err());
                assert_eq!(peak.load(Ordering::SeqCst), 4);
                assert_eq!(completed.load(Ordering::SeqCst), 4);
            });
    }
}
