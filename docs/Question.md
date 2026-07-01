Build a healthcare appointment management service that connects doctors and patients
using Go.
The service will enable:
● Patients to book and manage appointments with doctors.
● Doctors to maintain appointment notes, issue prescriptions, and communicate with
patients.
This document outlines the features the service should support. While we don't expect all
features to be fully implemented, consider them when designing your solution to ensure it
can accommodate future requirements.

Features
1. Appointments Management
Doctors and patients need to be able to manage their appointments.
Doctors need to be able to:
● Register available timeslots.
● See next appointments.
● Add notes to an appointment.
● Make follow-up prescriptions.
Patients need to be able to:
● See available timeslots for a doctor.
● Book up to one appointment with a doctor.
Both can get an overview of the appointment with all its information, including notes and
prescriptions.
2. Notes Streaming
When doctors start dictating notes for an appointment, the audio is transcribed by an
external service and the note chunks are streamed back (choose whatever protocol you
consider more appropriate).
The service must provide:
● An endpoint for doctors to start transcription for an appointment
● Background processing to consume the stream and store transcribed notes
Flow
1. Doctor initiates transcription for an appointment (via an endpoint in the service)
2. The service establishes a connection to the transcription server for that specific
appointment
3. The service listens to the stream, assembles chunks in sequence order, and stores
the complete notes
Event Format
Each event contains a payload with the following fields:
● appointmentId: Identifies which appointment this chunk belongs to
● sequence: Starts at 0 and increments for each chunk (use for ordering)
● text: The transcribed text chunk
● isFinal: When `true`, indicates this is the last chunk for the appointment
For example:
data: {"appointmentId": "123", "sequence": 0, "text": "transcribed
chunk", "isFinal": false}
data: {"appointmentId": "123", "sequence": 1, "text": "final chunk",
"isFinal": true}
3. Live Updates
Patients need to be able to register a webhook URL to receive real-time updates. When
events occur, the service sends HTTP POST requests to the registered webhook URL.
Webhook Payload Format
{
 "eventId": "unique-event-id",
 "eventType": "note_added | prescription_added",
 "timestamp": "2025-01-15T10:30:00Z",
 "appointmentId": "123",
 "patientId": "456",
  "data": {}
}
Event-Specific Data
note_added event:
{
 "noteId": "note-123",
 "noteText": "Patient reports..."
}
prescription_added event:
{
 "prescriptionId": "rx-456",
 "medication": "Aspirin 100mg",
 "expiresAt": "2025-02-15T00:00:00Z"
}
4. Historical Overview
The service needs to offer an historical overview, in order to have a better understanding of
how the clinical charts of a patient were looking at specific points in time.
Doctors need to be able to:
● Diagnose a new disease.
● Dismiss a diagnosis.
Both doctors and patients
Need to be able to get an overview of the patient at any point in time, including:
● Diagnosed diseases.
● Active prescriptions.
● Appointments with notes.
5. Pharmacist Medicine Dispatch
Allow pharmacists to dispatch medicines based on active prescriptions.
Prescription Rules:
● Prescriptions are **active** if they have not been used and have not expired.
● Prescriptions have an expiration date.
● A prescription becomes **unavailable** after dispatch.
6. Audit Trail
All changes to patient data must be auditable, so the service must track who made changes
and when.
7. Usage Analytics
In order to be able to get some patients analytics, the service needs to track usages for
every hospital organization and provide an endpoint to query:
● Total appointments
● Active patients
● Prescription counts
8. Multi-Tenancy
Each hospital organization will have their own independent system.
● Every tenant should be able to scale independently
● Tenants can have up to **10 million users**
● Tenants can be distributed across different regio