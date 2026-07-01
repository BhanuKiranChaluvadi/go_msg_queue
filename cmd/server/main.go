// Command server runs the medconnect API. By default it runs as a single binary
// with the transcription and webhook workers embedded; -embed-workers=false is
// reserved for running them as separate processes.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"medconnect/internal/analytics"
	"medconnect/internal/api"
	"medconnect/internal/appointments"
	"medconnect/internal/audit"
	"medconnect/internal/clinical"
	"medconnect/internal/domain"
	"medconnect/internal/events"
	"medconnect/internal/platform"
	"medconnect/internal/store/memory"
	"medconnect/internal/tenancy"
	"medconnect/internal/transcription"
	"medconnect/internal/webhooks"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	embedWorkers := flag.Bool("embed-workers", true, "run transcription and webhook workers in-process (false = split mode)")
	internalToken := flag.String("internal-token", os.Getenv("MEDCONNECT_INTERNAL_TOKEN"), "shared token guarding /internal/* endpoints")
	transcriptionURL := flag.String("transcription-url", os.Getenv("MEDCONNECT_TRANSCRIPTION_URL"), "base URL of the external transcription SSE server")
	transcriptionToken := flag.String("transcription-token", os.Getenv("MEDCONNECT_TRANSCRIPTION_TOKEN"), "bearer token for the transcription server")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	idgen := platform.NewRandomID()
	clock := platform.SystemClock{}
	eventStore := events.NewStore()
	publisher := events.NewPublisher(eventStore, clock, idgen)

	// Stores and services (in-memory core; swap for a SQL adapter in production).
	timeslotStore := memory.NewTimeslotStore()
	appointmentStore := memory.NewAppointmentStore()
	noteStore := memory.NewNoteStore()
	prescriptionStore := memory.NewPrescriptionStore()
	appts := appointments.NewService(appointments.Deps{
		Timeslots:     timeslotStore,
		Appointments:  appointmentStore,
		Notes:         noteStore,
		Prescriptions: prescriptionStore,
		Clock:         clock,
		IDGen:         idgen,
		Events:        publisher,
	})
	webhookRegistry := webhooks.NewRegistry(memory.NewWebhookStore(), idgen)
	auditSvc := audit.NewService(eventStore)
	analyticsSvc := analytics.NewService(eventStore)

	clinicalSvc := clinical.NewService(clinical.Deps{
		Diagnoses:     memory.NewDiagnosisStore(),
		Appointments:  appointmentStore,
		Notes:         noteStore,
		Prescriptions: prescriptionStore,
		Clock:         clock,
		IDGen:         idgen,
		Events:        publisher,
	})

	// Transcription worker: consumes the external SSE stream and stores dictated
	// notes. appts satisfies transcription.NoteStore.
	transcriptionMgr := transcription.NewManager(transcription.Config{
		Notes:  appts,
		Source: transcription.HTTPSource{BaseURL: *transcriptionURL, Token: *transcriptionToken},
		Logger: logger,
	})

	// Dev-only actor seed. Production replaces this with a user store / JWT auth.
	resolver := tenancy.StaticResolver{
		"doctor":     {ID: "doctor", TenantID: "demo", Role: domain.RoleDoctor},
		"patient":    {ID: "patient", TenantID: "demo", Role: domain.RolePatient},
		"pharmacist": {ID: "pharmacist", TenantID: "demo", Role: domain.RolePharmacist},
	}

	srv := &api.Server{
		Logger:        logger,
		IDGen:         idgen,
		Publisher:     publisher,
		InternalToken: *internalToken,
		Resolver:      resolver,
		Appointments:  appts,
		Webhooks:      webhookRegistry,
		Transcription: transcriptionMgr,
		Clinical:      clinicalSvc,
		Audit:         auditSvc,
		Analytics:     analyticsSvc,
	}

	// Live updates: the dispatcher delivers events to patient webhooks. In embedded
	// mode it runs in-process as a Publisher subscriber; in split mode a separate
	// cmd/notifier would consume /internal/events instead.
	dispatcher := webhooks.NewDispatcher(webhooks.Config{Logger: logger})
	if *embedWorkers {
		dispatcher.Start()
		publisher.Subscribe(webhooks.NewSubscriber(webhookRegistry, dispatcher, logger))
	}

	logger.Info("starting hub", "embedWorkers", *embedWorkers)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		logger.Error("failed to listen", "addr", *addr, "err", err)
		os.Exit(1)
	}

	if err := api.Serve(ctx, ln, srv.Handler(), logger); err != nil {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}

	// Drain in-flight webhook deliveries before exiting.
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transcriptionMgr.Stop(stopCtx)
	dispatcher.Stop(stopCtx)
	logger.Info("server stopped cleanly")
}
